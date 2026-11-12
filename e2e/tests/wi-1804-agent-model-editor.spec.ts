import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';

// WI-1804: changing the model selection while a save is in flight must not be
// recorded as saved. The completion handler has to reconcile from the
// connection that was submitted (and persisted), not the mutable selection.
test.describe('Agent model editor', () => {
  test('keeps a newer selection unsaved when it changes during save', async ({ page, request }) => {
    const workspace = await createWorkspaceViaAPI(
      request,
      generateWorkspace(`agent-model-${Date.now()}`)
    );
    const apiRoot = `**/api/workspaces/${workspace.id}`;
    const agentId = 501;

    const profile = {
      id: agentId,
      name: 'Workspace Guide',
      handle: 'workspace-guide',
      profile_type: 'standard',
      runtime: 'standard',
      lifecycle: 'draft',
      availability: 'draft',
      available: false,
      profile_version: 3,
      llm_connection_id: 11,
      model_summary: 'alpha-model',
      instructions: 'Help members find the right workspace context.',
      skill_ids: [],
      capability_groups: [],
      target_pool_id: null,
    };

    await page.route(`${apiRoot}/agent-profiles`, (route) => route.fulfill({ json: [profile] }));
    await page.route(`${apiRoot}/agent-bindings`, (route) => route.fulfill({ json: [profile] }));
    await page.route(`${apiRoot}/agent-skills`, (route) => route.fulfill({ json: [] }));
    await page.route(`${apiRoot}/agent-tool-capabilities`, (route) =>
      route.fulfill({ json: [] })
    );
    await page.route(`**/workspaces/${workspace.id}/agent-runs**`, (route) =>
      route.fulfill({ json: [] })
    );
    await page.route(`${apiRoot}/action-capabilities**`, (route) =>
      route.fulfill({ json: [] })
    );
    await page.route(`${apiRoot}/agent-profiles/${agentId}/validation`, (route) =>
      route.fulfill({ json: { ready: true, errors: [] } })
    );
    await page.route('**/api/llm/connections', (route) =>
      route.fulfill({
        json: [
          { id: 11, name: 'Alpha', model: 'alpha-model' },
          { id: 12, name: 'Beta', model: 'beta-model' },
          { id: 13, name: 'Gamma', model: 'gamma-model' },
        ],
      })
    );

    // Hold the update response so the test can change the selection while the
    // request is in flight, and record what the request actually submitted.
    let releaseUpdate!: () => void;
    const updateGate = new Promise<void>((resolve) => {
      releaseUpdate = resolve;
    });
    let signalUpdateSeen!: () => void;
    const updateSeen = new Promise<void>((resolve) => {
      signalUpdateSeen = resolve;
    });
    let submittedConnectionId: number | null = null;
    await page.route(`${apiRoot}/agent-bindings/${agentId}/agent-config`, async (route) => {
      submittedConnectionId = route.request().postDataJSON()?.llm_connection_id ?? null;
      signalUpdateSeen();
      await updateGate;
      await route.fulfill({
        json: { ...profile, llm_connection_id: submittedConnectionId },
      });
    });

    await page.goto(`/workspaces/${workspace.id}/agents/${agentId}`);
    await expect(page.getByTestId('agent-model-summary')).toContainText('alpha-model');

    await page.getByTestId('agent-tab-tools').click();
    await expect(page.getByTestId('agent-model')).toBeVisible();

    // Select Beta and save.
    await page.locator('#agent-model-connection').click();
    await page.locator('#agent-model-connection-option-12').click();
    await expect(page.getByTestId('agent-model-save')).toBeEnabled();
    await page.getByTestId('agent-model-save').click();
    await updateSeen;
    expect(submittedConnectionId).toBe(12);

    // While the save is pending, switch to Gamma.
    await page.locator('#agent-model-connection').click();
    await page.locator('#agent-model-connection-option-13').click();

    releaseUpdate();

    // The persisted model is Beta; Gamma stays unsaved.
    await expect(page.getByTestId('agent-model-save')).toBeEnabled();
    await expect(page.getByTestId('agent-model-saved')).toHaveCount(0);
    await page.getByTestId('agent-tab-overview').click();
    await expect(page.getByTestId('agent-model-summary')).toContainText('beta-model');
  });
});
