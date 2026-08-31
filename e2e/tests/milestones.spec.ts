import {
  createCollectionViaAPI,
  createItemViaAPI,
  createMilestoneViaAPI,
  createWorkspaceViaAPI,
  updateItemViaAPI,
} from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import {
  generateCollection,
  generateItem,
  generateMilestone,
  generateWorkspace,
} from '../fixtures/test-data';
import { ItemPage } from '../pages/item.page';
import { MilestonePage } from '../pages/milestone.page';

/**
 * Milestone management e2e coverage — mirrors the iterations spec:
 *   - Workspace-scoped and global creation via UI.
 *   - Status transition via the edit modal (planning → in-progress).
 *   - Delete via the row dropdown.
 *   - Assigning a milestone to a work item via the item-detail sidebar.
 *   - Filtering items by milestone_id via a collection's raw CQL query.
 */
test.describe('Milestone Management', () => {
  let milestonePage: MilestonePage;
  let itemPage: ItemPage;
  let workspaceId: number;

  test.beforeEach(async ({ page, request }) => {
    milestonePage = new MilestonePage(page);
    itemPage = new ItemPage(page);

    const testWorkspace = generateWorkspace();
    const created = await createWorkspaceViaAPI(request, {
      name: testWorkspace.name,
      key: testWorkspace.key,
      description: testWorkspace.description,
    });
    workspaceId = created.id;
  });

  test('creates a workspace milestone via the UI', async () => {
    const ms = generateMilestone('ws-create');

    await milestonePage.gotoWorkspace(workspaceId);
    await milestonePage.createMilestone({
      name: ms.name,
      target_date: ms.target_date,
      status: 'planning',
    });
    await milestonePage.verifyStatus(ms.name, 'Planning');
  });

  test('creates a global milestone via the UI from /milestones', async () => {
    const ms = generateMilestone('global-create');

    await milestonePage.gotoGlobal();
    await milestonePage.createMilestone({
      name: ms.name,
      target_date: ms.target_date,
      status: 'planning',
    });
    await milestonePage.verifyStatus(ms.name, 'Planning');
  });

  test('changes workspace milestone status from planning to in-progress via edit modal', async ({
    request,
  }) => {
    const ms = generateMilestone('status');
    await createMilestoneViaAPI(request, {
      ...ms,
      workspace_id: workspaceId,
    });

    await milestonePage.gotoWorkspace(workspaceId);
    await milestonePage.verifyStatus(ms.name, 'Planning');
    await milestonePage.changeStatusViaEdit(ms.name, 'in-progress');
    await milestonePage.verifyStatus(ms.name, 'In Progress');
  });

  test('deletes a workspace milestone via the row dropdown', async ({ request }) => {
    const ms = generateMilestone('delete');
    await createMilestoneViaAPI(request, {
      ...ms,
      workspace_id: workspaceId,
    });

    await milestonePage.gotoWorkspace(workspaceId);
    await milestonePage.verifyMilestoneExists(ms.name);
    await milestonePage.deleteMilestone(ms.name);
  });

  test('assigns a milestone to a work item via the item-detail sidebar', async ({
    page,
    request,
  }) => {
    const ms = generateMilestone('assign');
    const milestone = await createMilestoneViaAPI(request, {
      ...ms,
      workspace_id: workspaceId,
    });
    const itemData = generateItem(0, 'ms-assign');
    await createItemViaAPI(request, workspaceId, { title: itemData.title });

    await itemPage.gotoWorkspaceBacklog(String(workspaceId));
    await itemPage.openItemDetailModal(itemData.title);

    const dialog = page.locator('[role="dialog"]');
    await dialog.locator('[data-testid="milestone-field"]').click();
    await page.locator(`[role="option"][data-option-id="${milestone.id}"]`).click();
    await expect(dialog.locator('[data-testid="milestone-field"]')).toContainText(ms.name, {
      timeout: 5000,
    });
  });

  test('filters items by milestone via a collection using a raw CQL query', async ({
    page,
    request,
  }) => {
    const ms = generateMilestone('filter');
    const milestone = await createMilestoneViaAPI(request, {
      ...ms,
      workspace_id: workspaceId,
    });

    const includedItem = generateItem(0, 'in-ms');
    const excludedItem = generateItem(0, 'off-ms');
    const included = await createItemViaAPI(request, workspaceId, { title: includedItem.title });
    await createItemViaAPI(request, workspaceId, { title: excludedItem.title });
    // Items now have many milestones via the item_milestones junction; the
    // API field is `milestone_ids` (plural). The CQL filter below still works
    // because `milestone = N` matches any junction row with milestone_id = N.
    await updateItemViaAPI(request, included.id, { milestone_ids: [milestone.id] });

    const collectionData = generateCollection('by-milestone');
    const collection = await createCollectionViaAPI(request, {
      name: collectionData.name,
      description: collectionData.description,
      ql_query: `milestone_id = ${milestone.id}`,
      workspace_id: workspaceId,
    });

    // Use the collection-scoped backlog route so the backend applies the
    // stored ql_query server-side.
    await page.goto(`/workspaces/${workspaceId}/collections/${collection.id}/backlog`);
    await page.waitForLoadState('networkidle');

    await expect(page.getByText(includedItem.title).first()).toBeVisible({ timeout: 10000 });
    await expect(page.getByText(excludedItem.title)).toHaveCount(0);
  });
});
