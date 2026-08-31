import {
  authenticateAdminRequest,
  createCustomFieldViaAPI,
  createWorkspaceViaAPI,
} from '../fixtures/api-helpers';
import { expect, type Locator, type Page, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

async function cardFieldOrder(list: Locator): Promise<string[]> {
  return list
    .getByTestId(/^board-card-field-row-/)
    .evaluateAll((rows) => rows.map((row) => row.getAttribute('data-field-identifier') ?? ''));
}

async function dragHandleBeforeRow(page: Page, handle: Locator, target: Locator): Promise<void> {
  const handleBox = await handle.boundingBox();
  const targetBox = await target.boundingBox();
  if (!handleBox || !targetBox) {
    throw new Error('Card-field drag source and target must both be visible');
  }

  const startX = handleBox.x + handleBox.width / 2;
  const startY = handleBox.y + handleBox.height / 2;
  const targetX = targetBox.x + targetBox.width / 2;
  const targetY = targetBox.y + 2;
  await page.mouse.move(startX, startY);
  await page.mouse.down();
  await page.mouse.move(startX + 12, startY + 12);
  await page.mouse.move(targetX, targetY, { steps: 12 });
  await page.mouse.up();
}

test.describe('Board card fields', () => {
  test('groups custom field actions and options under one heading', async ({ page, request }) => {
    await authenticateAdminRequest(request);
    const stamp = Date.now();
    const workspace = await createWorkspaceViaAPI(
      request,
      generateWorkspace(`card-field-groups-${stamp}`)
    );
    const customField = await createCustomFieldViaAPI(request, {
      name: `Card field ${stamp}`,
      field_type: 'text',
    });

    await page.goto(`/workspaces/${workspace.id}/board/configure`);
    await page.getByTestId('board-config-card-fields-tab').click();

    const customSection = page.getByTestId('board-card-fields-custom-section');
    await expect(page.getByTestId('board-card-fields-custom-heading')).toHaveCount(1);
    await expect(customSection.getByTestId('board-custom-fields-action')).toBeVisible();
    await expect(
      customSection.getByTestId(`board-card-field-add-custom-${customField.id}`)
    ).toBeVisible();
  });

  test('dragging a field to a new position persists the displayed order', async ({
    page,
    request,
  }) => {
    await authenticateAdminRequest(request);
    const workspace = await createWorkspaceViaAPI(
      request,
      generateWorkspace(`card-field-order-${Date.now()}`)
    );

    const createConfig = await request.post(
      `/api/collections/default/board-configuration?workspace_id=${workspace.id}`,
      {
        headers: SEC_FETCH,
        data: {
          columns: [],
          backlog_status_ids: [],
          list_columns: [],
          card_fields: [
            {
              field_identifier: 'priority',
              field_type: 'system',
              display_order: 0,
            },
            {
              field_identifier: 'due_date',
              field_type: 'system',
              display_order: 1,
            },
            {
              field_identifier: 'labels',
              field_type: 'system',
              display_order: 2,
            },
          ],
        },
      }
    );
    expect(createConfig.ok(), `create board config failed (${createConfig.status()})`).toBeTruthy();

    const configureURL = `/workspaces/${workspace.id}/board/configure`;
    await page.goto(configureURL);
    await page.getByTestId('board-config-card-fields-tab').click();

    const list = page.getByTestId('board-card-fields-list');
    const priorityRow = page.getByTestId('board-card-field-row-priority');
    const labelsRow = page.getByTestId('board-card-field-row-labels');
    await expect(labelsRow).toHaveAttribute('draggable', 'true', {
      timeout: 5_000,
    });
    await expect.poll(() => cardFieldOrder(list)).toEqual(['priority', 'due_date', 'labels']);

    const priorityHandle = priorityRow.getByTestId('board-card-field-drag-handle');
    await priorityHandle.press('ArrowDown');
    await expect.poll(() => cardFieldOrder(list)).toEqual(['due_date', 'priority', 'labels']);
    await priorityHandle.press('ArrowUp');
    await expect.poll(() => cardFieldOrder(list)).toEqual(['priority', 'due_date', 'labels']);

    await dragHandleBeforeRow(
      page,
      labelsRow.getByTestId('board-card-field-drag-handle'),
      priorityRow
    );
    await expect.poll(() => cardFieldOrder(list)).toEqual(['labels', 'priority', 'due_date']);

    await page.getByTestId('board-config-save').click();
    await page.waitForURL((url) => url.pathname === `/workspaces/${workspace.id}/board`);

    await page.goto(configureURL);
    await page.getByTestId('board-config-card-fields-tab').click();
    await expect
      .poll(() => cardFieldOrder(page.getByTestId('board-card-fields-list')))
      .toEqual(['labels', 'priority', 'due_date']);
  });
});
