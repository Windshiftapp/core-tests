import { expect, test } from '../fixtures/context-path';
import {
  createCollectionViaAPI,
  createItemViaAPI,
  createMilestoneViaAPI,
  createWorkspaceViaAPI,
  updateItemViaAPI,
} from '../fixtures/api-helpers';
import {
  generateCollection,
  generateItem,
  generateMilestone,
  generateWorkspace,
} from '../fixtures/test-data';
import { MilestonePage } from '../pages/milestone.page';
import { ItemPage } from '../pages/item.page';

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

  test('changes workspace milestone status from planning to in-progress via edit modal', async ({ request }) => {
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

  test('assigns a milestone to a work item via the item-detail sidebar', async ({ page, request }) => {
    const ms = generateMilestone('assign');
    const milestone = await createMilestoneViaAPI(request, {
      ...ms,
      workspace_id: workspaceId,
    });
    // Items are assignable to both scopes, so the picker must offer a global
    // milestone alongside the workspace's own.
    const globalMs = generateMilestone('assign-global');
    const globalMilestone = await createMilestoneViaAPI(request, {
      ...globalMs,
      workspace_id: null,
    });
    const itemData = generateItem(0, 'ms-assign');
    await createItemViaAPI(request, workspaceId, { title: itemData.title });

    await itemPage.gotoWorkspaceBacklog(String(workspaceId));
    await itemPage.openItemDetailModal(itemData.title);

    const dialog = page.locator('[role="dialog"]');
    await dialog.locator('[data-testid="milestone-field"]').click();
    await expect(page.locator(`[role="option"][data-option-id="${milestone.id}"]`)).toBeVisible();
    await expect(
      page.locator(`[role="option"][data-option-id="${globalMilestone.id}"]`)
    ).toBeVisible();
    await page.locator(`[role="option"][data-option-id="${milestone.id}"]`).click();
    await expect(dialog.locator('[data-testid="milestone-field"]')).toContainText(ms.name, {
      timeout: 5000,
    });
  });

  test('offers the new workspace milestones after an in-app workspace switch', async ({
    page,
    request,
  }) => {
    // Workspace A (from beforeEach) has no milestones. Load one of its items
    // first so the shared item-detail store holds A's empty milestone list.
    const itemA = generateItem(0, 'switch-a');
    await createItemViaAPI(request, workspaceId, { title: itemA.title });

    // Workspace B owns a milestone and an item.
    const workspaceB = generateWorkspace();
    const createdB = await createWorkspaceViaAPI(request, {
      name: workspaceB.name,
      key: workspaceB.key,
      description: workspaceB.description,
    });
    const workspaceBId = createdB.id;
    const ms = generateMilestone('switch-b');
    const milestone = await createMilestoneViaAPI(request, {
      ...ms,
      workspace_id: workspaceBId,
    });
    const itemB = generateItem(0, 'switch-b');
    await createItemViaAPI(request, workspaceBId, { title: itemB.title });

    await itemPage.gotoWorkspaceBacklog(String(workspaceId));
    await itemPage.openItemDetailModal(itemA.title);
    await itemPage.closeItemDetailModal();

    // Switch through the in-app workspace menu (no reload), then open B's
    // item. The detail store briefly pairs B's item with A's references; the
    // milestone picker must not lock onto A's empty list.
    await page.getByTestId('workspaces-dropdown-trigger').click();
    await page.getByTestId('workspaces-search').fill(workspaceB.name);
    await page
      .getByTestId('workspace-dropdown-item')
      .filter({ hasText: workspaceB.name })
      .first()
      .click();
    await page.getByTestId('workspace-nav-backlog').click();

    await itemPage.openItemDetailModal(itemB.title);

    // The modal first reuses the previous item while the new detail loads.
    // Wait until it actually shows B's item before opening the picker.
    const dialog = page.locator('[role="dialog"]');
    await expect(dialog.getByText(itemB.title).first()).toBeVisible();
    await dialog.locator('[data-testid="milestone-field"]').click();
    await expect(page.locator(`[role="option"][data-option-id="${milestone.id}"]`)).toBeVisible();
  });

  test('filters items by milestone via a collection using a raw CQL query', async ({ page, request }) => {
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


    await expect(page.getByText(includedItem.title).first()).toBeVisible({ timeout: 10000 });
    await expect(page.getByText(excludedItem.title)).toHaveCount(0);
  });
});

