import { createItemViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

test.describe('Work-item diagram deletion', () => {
  test('deletes a diagram from the item detail', async ({ page, request }) => {
    const workspace = await createWorkspaceViaAPI(request, generateWorkspace('diagram-delete'));
    const item = await createItemViaAPI(request, workspace.id, {
      title: `Diagram deletion ${Date.now()}`,
    });
    const createResponse = await request.post(`/api/items/${item.id}/diagrams`, {
      headers: SEC_FETCH,
      data: {
        name: 'Disposable architecture',
        diagram_data: JSON.stringify({ elements: [], appState: {}, files: {} }),
      },
    });
    expect(
      createResponse.ok(),
      `create diagram failed (${createResponse.status()}): ${await createResponse.text()}`
    ).toBeTruthy();
    const diagram = (await createResponse.json()) as { id: number };

    await page.goto(`/workspaces/${workspace.id}/items/${item.id}`);
    await expect(page.getByTestId('item-detail-ready')).toBeVisible();
    const diagramRow = page.getByTestId(`item-diagram-${diagram.id}`);
    await expect(diagramRow).toBeVisible({ timeout: 10_000 });
    await diagramRow.hover();

    await page.getByTestId(`item-diagram-delete-${diagram.id}`).click();
    await expect(page.getByTestId('dialog-confirm')).toBeVisible();
    await page.getByTestId('dialog-cancel').click();
    await expect(diagramRow).toBeVisible();

    const deleteRoute = `**/api/diagrams/${diagram.id}`;
    await page.route(deleteRoute, (route) =>
      route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'forced delete failure' }),
      })
    );
    await page.getByTestId(`item-diagram-delete-${diagram.id}`).click();
    await page.getByTestId('dialog-confirm').click();
    await expect(page.getByTestId('toast-message-error')).toHaveText('Failed to delete diagram');
    await expect(diagramRow).toBeVisible();
    await page.unroute(deleteRoute);

    await page.getByTestId(`item-diagram-delete-${diagram.id}`).click();
    await expect(page.getByTestId('dialog-confirm')).toBeVisible();

    const deleteResponse = page.waitForResponse(
      (response) =>
        response.request().method() === 'DELETE' &&
        response.url().endsWith(`/api/diagrams/${diagram.id}`) &&
        response.ok()
    );
    await page.getByTestId('dialog-confirm').click();
    await deleteResponse;

    await expect(diagramRow).toHaveCount(0);
    await page.reload();
    await expect(page.getByTestId(`item-diagram-${diagram.id}`)).toHaveCount(0);
  });
});
