import { authenticateAdminRequest, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

test.describe('Board configuration columns', () => {
  test('keeps the column name editable and the WIP limit compact', async ({ page, request }) => {
    await authenticateAdminRequest(request);
    const workspace = await createWorkspaceViaAPI(
      request,
      generateWorkspace(`board-column-fields-${Date.now()}`)
    );

    const createConfig = await request.post(
      `/api/collections/default/board-configuration?workspace_id=${workspace.id}`,
      {
        headers: SEC_FETCH,
        data: {
          columns: [
            {
              name: 'In progress',
              display_order: 0,
              wip_limit: 4,
              color: '#f3f4f6',
              status_ids: [],
            },
          ],
          backlog_status_ids: [],
          list_columns: [],
          card_fields: [],
        },
      }
    );
    expect(createConfig.ok(), `create board config failed (${createConfig.status()})`).toBeTruthy();

    const configureURL = `/workspaces/${workspace.id}/board/configure`;
    await page.goto(configureURL);

    const nameInput = page.getByTestId('board-column-name-0');
    const wipInput = page.getByTestId('board-column-wip-0');
    await expect(nameInput).toHaveValue('In progress');
    await expect(wipInput).toHaveValue('4');

    const nameBox = await nameInput.boundingBox();
    const wipBox = await wipInput.boundingBox();
    if (!nameBox || !wipBox) {
      throw new Error('Column configuration inputs should be visible');
    }
    expect(nameBox.width).toBeGreaterThan(wipBox.width * 2);

    await nameInput.fill('Ready for review');
    await wipInput.fill('7');
    await page.getByTestId('board-config-save').click();
    await page.waitForURL((url) => url.pathname === `/workspaces/${workspace.id}/board`);

    await page.goto(configureURL);
    await expect(page.getByTestId('board-column-name-0')).toHaveValue('Ready for review');
    await expect(page.getByTestId('board-column-wip-0')).toHaveValue('7');
  });

  test('persists age trimming only when the fixed rightmost limit is off', async ({
    page,
    request,
  }) => {
    await authenticateAdminRequest(request);
    const workspace = await createWorkspaceViaAPI(
      request,
      generateWorkspace(`board-completed-retention-${Date.now()}`)
    );
    const configureURL = `/workspaces/${workspace.id}/board/configure`;

    await page.goto(configureURL);
    const fixedLimit = page.getByTestId('board-rightmost-limit-enabled');
    const ageLimit = page.getByTestId('board-completed-age-enabled');
    await expect(ageLimit).toBeVisible();

    await fixedLimit.click();
    await expect(ageLimit).toHaveCount(0);
    await fixedLimit.click();
    await expect(ageLimit).toBeVisible();

    await ageLimit.click();
    const retentionDays = page.getByTestId('board-completed-retention-days');
    await retentionDays.fill('45');

    const filteredItemsRequest = page.waitForRequest((candidate) => {
      if (!candidate.url().includes('/api/items?')) return false;
      return new URL(candidate.url()).searchParams.has('completed_activity_days');
    });
    await page.getByTestId('board-config-save').click();
    const filteredRequest = await filteredItemsRequest;
    await page.waitForURL((url) => url.pathname === `/workspaces/${workspace.id}/board`);

    expect(new URL(filteredRequest.url()).searchParams.get('completed_activity_days')).toBe('45');

    let configResponse = await request.get(
      `/api/collections/default/board-configuration?workspace_id=${workspace.id}`,
      { headers: SEC_FETCH }
    );
    expect(configResponse.ok()).toBeTruthy();
    let config = await configResponse.json();
    expect(config.show_rightmost_column_last_50).toBe(false);
    expect(config.completed_item_retention_days).toBe(45);

    await page.goto(configureURL);
    await expect(page.getByTestId('board-completed-retention-days')).toHaveValue('45');
    await page.getByTestId('board-rightmost-limit-enabled').click();
    await expect(page.getByTestId('board-completed-age-enabled')).toHaveCount(0);
    await page.getByTestId('board-config-save').click();
    await page.waitForURL((url) => url.pathname === `/workspaces/${workspace.id}/board`);

    configResponse = await request.get(
      `/api/collections/default/board-configuration?workspace_id=${workspace.id}`,
      { headers: SEC_FETCH }
    );
    expect(configResponse.ok()).toBeTruthy();
    config = await configResponse.json();
    expect(config.show_rightmost_column_last_50).toBe(true);
    expect(config.completed_item_retention_days ?? null).toBeNull();
  });
});
