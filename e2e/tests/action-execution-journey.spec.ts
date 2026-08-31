import { createItemViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { type APIRequestContext, expect, type Page, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

type ActionNode = {
  id: number;
  node_type: string;
  node_config: string;
  position_x: number;
  position_y: number;
};

async function createManualAction(
  request: APIRequestContext,
  workspaceID: number,
  name: string,
  node: ActionNode
) {
  const response = await request.post(`/api/workspaces/${workspaceID}/actions`, {
    headers: SEC_FETCH,
    data: {
      name,
      description: `Browser execution journey for ${name}`,
      trigger_type: 'manual',
      trigger_config: '{}',
      nodes: [
        {
          id: 1,
          node_type: 'trigger',
          node_config: '{}',
          position_x: 0,
          position_y: 0,
        },
        node,
      ],
      edges: [
        {
          id: 0,
          source_node_id: 1,
          target_node_id: node.id,
          edge_type: 'default',
        },
      ],
    },
  });
  expect(response.status(), `create action ${name}: ${await response.text()}`).toBe(201);
  return response.json();
}

async function executeActionFromUI(
  page: Page,
  actionID: number,
  itemID: number,
  itemTitle: string
) {
  const actionCard = page.getByTestId(`action-card-${actionID}`);
  await expect(actionCard).toBeVisible();
  await actionCard.getByTestId('action-test').click();
  await page.getByTestId('item-picker-trigger').click();
  const searchInput = page.getByTestId('action-test-item-search');
  await expect(searchInput).toBeVisible();
  await searchInput.fill(itemTitle);
  const itemOption = page.getByTestId(`action-test-item-${itemID}`);
  await expect(itemOption).toBeVisible();
  await itemOption.click();
  await page.getByTestId('action-test-execute').click();
  await expect(page.getByTestId('action-test-execute')).toHaveCount(0);
}

test('manual actions show successful and failed logs, redact URL secrets, and can be retriggered', async ({
  request,
  page,
}, testInfo) => {
  test.setTimeout(90_000);
  const stamp = `${Date.now()}-${testInfo.workerIndex}-${testInfo.repeatEachIndex}`;
  const workspace = await createWorkspaceViaAPI(
    request,
    generateWorkspace(`action-runtime-${stamp}`)
  );
  const itemTitle = `Action runtime item ${stamp}`;
  const item = await createItemViaAPI(request, workspace.id, {
    title: itemTitle,
    description: `Action runtime description ${stamp}`,
  });
  const successComment = `Action completed visibly ${stamp}`;

  const successAction = await createManualAction(
    request,
    workspace.id,
    `Successful manual action ${stamp}`,
    {
      id: 2,
      node_type: 'add_comment',
      node_config: JSON.stringify({
        content: successComment,
        is_private: false,
      }),
      position_x: 240,
      position_y: 0,
    }
  );

  const capabilityResponse = await request.post('/api/admin/action-capabilities', {
    headers: SEC_FETCH,
    data: {
      name: `Failure probe ${stamp}`,
      capability_type: 'http_client',
      config: JSON.stringify({
        allowed_url_patterns: ['https://unresolvable.invalid/**'],
        timeout_secs: 2,
      }),
      applies_to_all_workspaces: false,
      workspace_ids: [workspace.id],
    },
  });
  expect(
    capabilityResponse.status(),
    `create failure capability: ${await capabilityResponse.text()}`
  ).toBe(201);
  const capability = await capabilityResponse.json();
  const secret = `never-render-${stamp}`;
  const failureAction = await createManualAction(
    request,
    workspace.id,
    `Failing manual action ${stamp}`,
    {
      id: 2,
      node_type: 'http_request',
      node_config: JSON.stringify({
        method: 'GET',
        url_template: `https://unresolvable.invalid/probe?token=${secret}`,
        headers: {},
        body: '',
        output_field: 'probe',
        capability_id: capability.id,
      }),
      position_x: 240,
      position_y: 0,
    }
  );

  await page.goto(`/workspaces/${workspace.id}/actions`);
  await executeActionFromUI(page, successAction.id, item.id, itemTitle);

  const successCard = page.getByTestId(`action-card-${successAction.id}`);
  await successCard.getByTestId('action-view-logs').click();
  const successLogs = page.getByTestId('action-logs');
  await expect(successLogs).toBeVisible();
  await expect(successLogs.getByTestId(/^action-log-row-/)).toHaveCount(1);
  await expect(successLogs).toContainText('Completed');

  await page.goto(`/workspaces/${workspace.id}/items/${item.id}`);
  await expect(page.getByTestId('comments-section')).toContainText(successComment);

  await page.goto(`/workspaces/${workspace.id}/actions`);
  await executeActionFromUI(page, failureAction.id, item.id, itemTitle);
  await executeActionFromUI(page, failureAction.id, item.id, itemTitle);

  const failureCard = page.getByTestId(`action-card-${failureAction.id}`);
  await failureCard.getByTestId('action-view-logs').click();
  const failureLogs = page.getByTestId('action-logs');
  await expect(failureLogs).toBeVisible();
  await expect(failureLogs.getByTestId(/^action-log-row-/)).toHaveCount(2);
  await expect(failureLogs).toContainText('Failed');

  await failureLogs.getByTestId('action-log-details').first().click();
  const trace = page.getByTestId('action-execution-trace');
  await expect(trace).toBeVisible();
  await expect(trace).toContainText('Failed');
  await expect(trace).toContainText('https://unresolvable.invalid/probe');
  await expect(trace).not.toContainText(secret);
});
