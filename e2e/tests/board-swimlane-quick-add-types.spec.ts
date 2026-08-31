import { createWorkspaceViaAPI, listItemTypesViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { BoardPage } from '../pages/board.page';

/**
 * Swimlane quick-add offers only the item types that can legally be created
 * under the lane's item. Regular child types sit exactly one hierarchy level
 * below their parent, while the generic Sub-task type is valid below any
 * regular parent. Picking anything else used to fail with a hierarchy error
 * after the user had already typed a title.
 */
test.describe('Board swimlane quick-add item types', () => {
  test('only offers child types of the swimlane item type', async ({ page, request }) => {
    const workspace = generateWorkspace();
    const created = await createWorkspaceViaAPI(request, {
      name: workspace.name,
      key: workspace.key,
      description: workspace.description ?? 'swimlane quick-add types',
    });
    const workspaceId = created.id as number;

    const itemTypes = await listItemTypesViaAPI(request);
    const byLevel = (level: number) =>
      itemTypes.filter((type) => (type.hierarchy_level ?? 0) === level);

    // Pick a level that has at least one type below it, so the lane has options.
    const parentType = byLevel(1)[0] ?? byLevel(0)[0];
    expect(parentType, 'no item type to group by').toBeTruthy();
    const genericSubtaskTypes = byLevel(-1);
    expect(genericSubtaskTypes.length, 'no generic sub-task type').toBeGreaterThan(0);
    const expectedChildTypes = [
      ...byLevel((parentType.hierarchy_level ?? 0) + 1),
      ...genericSubtaskTypes,
    ];
    expect(expectedChildTypes.length, 'no child types for the grouping type').toBeGreaterThan(0);

    const itemResp = await request.post('/api/items', {
      headers: { 'Sec-Fetch-Site': 'same-origin' },
      data: {
        workspace_id: workspaceId,
        item_type_id: parentType.id,
        title: `Swimlane parent ${Date.now()}`,
      },
    });
    expect(itemResp.ok(), `item create: ${itemResp.status()}`).toBeTruthy();

    const boardPage = new BoardPage(page);
    await boardPage.goto(String(workspaceId));
    await boardPage.verifyBoardVisible();

    // Group by the parent's item type so its item becomes a swimlane.
    await page.locator('[data-testid="board-group-by-menu"]').click();
    await page.locator(`[data-testid="board-group-by-type-${parentType.id}"]`).click();

    const lane = page.locator(`[data-board-swimlane^="parent-"]`).first();
    await expect(lane).toBeVisible();

    await lane.locator('[data-testid^="board-column-add-"]').first().click();
    await lane.locator('[data-testid="quick-add-type"]').click();

    const options = page.locator('[data-quick-add-menu] [data-testid^="quick-add-type-option-"]');
    await expect(options).toHaveCount(expectedChildTypes.length);
    for (const childType of expectedChildTypes) {
      await expect(
        page.locator(`[data-testid="quick-add-type-option-${childType.id}"]`)
      ).toBeVisible();
    }

    // The unassigned lane parents nothing, so it offers all regular types but
    // not the generic Sub-task type, which must have a parent.
    await lane.locator('[data-testid="quick-add-cancel"]').click();
    const unassignedLane = page.locator('[data-board-swimlane="unassigned"]');
    await unassignedLane.locator('[data-testid^="board-column-add-"]').first().click();
    await unassignedLane.locator('[data-testid="quick-add-type"]').click();
    await expect(options).toHaveCount(itemTypes.length - genericSubtaskTypes.length);
    for (const genericSubtaskType of genericSubtaskTypes) {
      await expect(
        page.locator(`[data-testid="quick-add-type-option-${genericSubtaskType.id}"]`)
      ).toHaveCount(0);
    }
  });
});
