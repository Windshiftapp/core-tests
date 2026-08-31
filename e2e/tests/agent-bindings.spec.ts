import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';

/**
 * Agent bindings journey under Agent Studio (WI-900). The Studio permanently
 * owns the former Coding Agents settings destination and exposes agent
 * profiles from /workspaces/<id>/agents. The full create round-trip needs a
 * configured runner + LLM connection (covered at the service layer), so this
 * spec pins the studio shell: the catalog as the legacy redirect target, the
 * create journey opening and closing cleanly, and the coding profile's
 * primary-repository picker, acting-identity picker, and base-ref prefill
 * that replaced the old WI-449 multi-repo editor.
 */

test.describe('Agent Studio bindings', () => {
  let workspace: Awaited<ReturnType<typeof createWorkspaceViaAPI>>;

  test.beforeEach(async ({ request }) => {
    workspace = await createWorkspaceViaAPI(request, generateWorkspace('agent-bindings'));
  });

  test('the former Coding Agents settings URL redirects to the catalog with an empty state', async ({
    page,
  }) => {
    await page.goto(`/workspaces/${workspace.id}/settings/coding-agents`);
    await expect(page).toHaveURL(/\/agents$/);
    await expect(page.getByTestId('agent-catalog')).toBeVisible({ timeout: 5000 });
    await expect(page.getByTestId('agent-catalog-empty-manage')).toBeVisible();
  });

  test('the create journey opens from the catalog and cancels cleanly without a draft', async ({
    page,
  }) => {
    await page.goto(`/workspaces/${workspace.id}/agents`);
    await page.getByTestId('agent-catalog-empty-manage').click({ timeout: 5000 });
    await expect(page.getByTestId('agent-create')).toBeVisible({ timeout: 5000 });
    await expect(page.getByTestId('agent-template').first()).toBeVisible();

    await page.getByTestId('agent-create-back').click();
    await expect(page.getByTestId('agent-create')).toHaveCount(0);
    await expect(page.getByTestId('agent-catalog')).toBeVisible({ timeout: 5000 });
    await expect(page).toHaveURL(/\/agents$/);
    const leftoverDraft = await page.evaluate(() =>
      Object.keys(window.localStorage).some((key) => key.startsWith('agent-studio-create:'))
    );
    expect(leftoverDraft).toBe(false);
  });

  test('coding profile exposes the repository, identity, and base-ref pickers before saving', async ({
    page,
  }) => {
    const apiRoot = `**/api/workspaces/${workspace.id}`;
    await page.route(`${apiRoot}/agent-templates`, (route) =>
      route.fulfill({
        json: [
          {
            key: 'software_engineer',
            name: 'Software engineer',
            default_type: 'coding',
            instructions: 'Write code in the configured repository.',
          },
        ],
      })
    );
    await page.route('**/api/llm/connections', (route) =>
      route.fulfill({ json: [{ id: 9, name: 'Primary model', model: 'test-model' }] })
    );
    await page.route(`${apiRoot}/agent-binding-candidates`, (route) =>
      route.fulfill({ json: [{ user_id: 42, name: 'Release Bot', username: 'release-bot' }] })
    );
    await page.route(`${apiRoot}/agent-tool-capabilities`, (route) => route.fulfill({ json: [] }));
    await page.route(`${apiRoot}/scm-connections`, (route) =>
      route.fulfill({
        json: [{ id: 5, name: 'GitHub', provider_slug: 'github', is_connected: true }],
      })
    );
    await page.route(`${apiRoot}/scm-connections/5/repositories`, (route) =>
      route.fulfill({
        json: [
          {
            id: 11,
            repository_name: 'docs-platform',
            repository_url: 'https://github.com/acme/docs-platform',
            default_branch: 'main',
          },
        ],
      })
    );
    await page.route(`${apiRoot}/action-capabilities?type=runner_pool`, (route) =>
      route.fulfill({ json: [] })
    );

    await page.goto(`/workspaces/${workspace.id}/agents/new`);
    await expect(page.getByTestId('agent-create')).toBeVisible({ timeout: 5000 });

    // The software-engineer template is selected first, which activates the
    // coding profile and its repository section.
    await expect(page.locator('#agent-create-repository')).toBeVisible({ timeout: 5000 });
    await expect(page.getByTestId('agent-create-base-ref')).toBeVisible();
    // The template's reviewed instructions become the profile's starting prompt.
    await expect(page.getByTestId('agent-create-instructions')).toHaveValue(
      'Write code in the configured repository.'
    );

    // A centralized service identity surfaces in the acting-identity picker.
    await expect(page.locator('#agent-create-identity')).toBeVisible();
    await page.locator('#agent-create-identity').click();
    await expect(page.locator('#agent-create-identity-option-42')).toBeVisible();
    await page.keyboard.press('Escape');

    // The single primary repository replaces the old multi-repo editor; its
    // default branch pre-fills the base ref.
    await page.locator('#agent-create-repository').click();
    await page.locator('#agent-create-repository-option-5-11').click();
    await expect(page.getByTestId('agent-create-base-ref')).toHaveValue('main');

    // Required fields gate submission: clearing the pre-filled template name
    // disables it again.
    await expect(page.getByTestId('agent-create-submit')).toBeEnabled();
    await page.getByTestId('agent-create-name').fill('');
    await expect(page.getByTestId('agent-create-submit')).toBeDisabled();
    await page.getByTestId('agent-create-name').fill('Docs Bot');
    await page.getByTestId('agent-create-handle').fill('docs-bot');
    await expect(page.getByTestId('agent-create-submit')).toBeEnabled();

    await page.getByTestId('agent-create-back').click();
    await expect(page.getByTestId('agent-catalog')).toBeVisible({ timeout: 5000 });
    const leftoverDraft = await page.evaluate(() =>
      Object.keys(window.localStorage).some((key) => key.startsWith('agent-studio-create:'))
    );
    expect(leftoverDraft).toBe(false);
  });
});
