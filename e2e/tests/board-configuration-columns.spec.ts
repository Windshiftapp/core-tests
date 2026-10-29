import { authenticateAdminRequest, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

function hexToRgb(hex: string): string {
  const value = hex.replace('#', '');
  const r = Number.parseInt(value.slice(0, 2), 16);
  const g = Number.parseInt(value.slice(2, 4), 16);
  const b = Number.parseInt(value.slice(4, 6), 16);
  return `rgb(${r}, ${g}, ${b})`;
}

test.describe('Board configuration columns', () => {
  test('keeps the column name editable and the WIP limit compact', async ({ page, request }) => {
    await authenticateAdminRequest(request);
    const workspace = await createWorkspaceViaAPI(
      request,
      generateWorkspace(`board-column-fields-${Date.now()}`)
    );

    const createConfig = await request.put(
      `/api/v2/workspaces/${workspace.id}/board-configuration`,
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

  test('lets an administrator pick a column color and persists it', async ({ page, request }) => {
    await authenticateAdminRequest(request);
    const workspace = await createWorkspaceViaAPI(
      request,
      generateWorkspace(`board-column-color-${Date.now()}`)
    );

    const createConfig = await request.put(
      `/api/v2/workspaces/${workspace.id}/board-configuration`,
      {
        headers: SEC_FETCH,
        data: {
          columns: [
            {
              name: 'Ready',
              display_order: 0,
              wip_limit: null,
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

    const colorTrigger = page.getByTestId('board-column-color-0-trigger');
    await expect(colorTrigger).toHaveCSS('background-color', 'rgb(243, 244, 246)');

    await colorTrigger.click();
    const colorOption = page.getByTestId('board-column-color-0-color').last();
    const expectedColor = await colorOption.getAttribute('title');
    expect(expectedColor, 'color option should expose its hex value').toBeTruthy();
    await colorOption.click();

    await page.getByTestId('board-config-save').click();
    await page.waitForURL((url) => url.pathname === `/workspaces/${workspace.id}/board`);

    const configResponse = await request.get(
      `/api/v2/workspaces/${workspace.id}/board-configuration`,
      { headers: SEC_FETCH }
    );
    expect(configResponse.ok()).toBeTruthy();
    const config = (await configResponse.json()).data;
    expect(config.columns[0].color).toBe(expectedColor);

    await page.goto(configureURL);
    await expect(page.getByTestId('board-column-color-0-trigger')).toHaveCSS(
      'background-color',
      hexToRgb(expectedColor as string)
    );
  });

  test('persists the completed-work preset and its activity window', async ({
    page,
    request,
  }) => {
    await authenticateAdminRequest(request);
    const workspace = await createWorkspaceViaAPI(
      request,
      generateWorkspace(`board-completed-preset-${Date.now()}`)
    );
    const configureURL = `/workspaces/${workspace.id}/board/configure`;

    await page.goto(configureURL);
    // The legacy age-trimming and rightmost-cap controls are superseded by the
    // preset select.
    await expect(page.getByTestId('board-completed-age-enabled')).toHaveCount(0);
    await expect(page.getByTestId('board-completed-retention-days')).toHaveCount(0);
    await expect(page.getByTestId('board-rightmost-limit-enabled')).toHaveCount(0);

    await page.getByTestId('board-completed-visibility').selectOption('last_week');

    const filteredItemsRequest = page.waitForRequest((candidate) => {
      if (!candidate.url().includes('/api/v2/items?')) return false;
      return new URL(candidate.url()).searchParams.get('completed_activity_days') === '7';
    });
    await page.getByTestId('board-config-save').click();
    await filteredItemsRequest;
    await page.waitForURL((url) => url.pathname === `/workspaces/${workspace.id}/board`);

    let configResponse = await request.get(
      `/api/v2/workspaces/${workspace.id}/board-configuration`,
      { headers: SEC_FETCH }
    );
    expect(configResponse.ok()).toBeTruthy();
    let config = (await configResponse.json()).data;
    expect(config.view_settings?.completed_visibility).toBe('last_week');
    expect(config.completed_item_retention_days).toBe(7);
    expect(config.show_rightmost_column_last_50).toBe(false);

    // The preset round-trips into the select.
    await page.goto(configureURL);
    await expect(page.getByTestId('board-completed-visibility')).toHaveValue('last_week');

    // A preset without a window clears the legacy retention days.
    await page.getByTestId('board-completed-visibility').selectOption('all');
    await page.getByTestId('board-config-save').click();
    await page.waitForURL((url) => url.pathname === `/workspaces/${workspace.id}/board`);

    configResponse = await request.get(
      `/api/v2/workspaces/${workspace.id}/board-configuration`,
      { headers: SEC_FETCH }
    );
    expect(configResponse.ok()).toBeTruthy();
    config = (await configResponse.json()).data;
    expect(config.view_settings?.completed_visibility).toBe('all');
    expect(config.completed_item_retention_days ?? null).toBeNull();
  });
});
