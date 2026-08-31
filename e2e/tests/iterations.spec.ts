import {
  createCollectionViaAPI,
  createItemViaAPI,
  createIterationViaAPI,
  createWorkspaceViaAPI,
  listIterationTypesViaAPI,
  updateItemViaAPI,
} from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import {
  generateCollection,
  generateItem,
  generateIteration,
  generateWorkspace,
} from '../fixtures/test-data';
import { ItemPage } from '../pages/item.page';
import { IterationPage } from '../pages/iteration.page';

/**
 * Iteration management e2e coverage:
 *   - Workspace-scoped and global iteration creation via UI.
 *   - Status transition via the edit modal.
 *   - Delete via the row dropdown.
 *   - Assigning an iteration to a work item via the item-detail sidebar.
 *   - Filtering items by iteration_id via a collection's raw CQL query.
 *
 * Scope-sensitive bits (global vs workspace) exercise both PUT routes — the
 * API helper picks `/global/iterations/:id` or `/workspaces/:ws/iterations/:id`
 * based on `is_global`, so an incorrect is_global flag on create surfaces
 * later when edit fails.
 */
test.describe('Iteration Management', () => {
  let iterationPage: IterationPage;
  let itemPage: ItemPage;
  let workspaceId: number;
  let iterationTypeId: number;

  test.beforeAll(async ({ request }) => {
    const types = await listIterationTypesViaAPI(request);
    if (types.length === 0) {
      throw new Error('No iteration types seeded — backend seed changed?');
    }
    iterationTypeId = types[0].id;
  });

  test.beforeEach(async ({ page, request }) => {
    iterationPage = new IterationPage(page);
    itemPage = new ItemPage(page);

    const testWorkspace = generateWorkspace();
    const created = await createWorkspaceViaAPI(request, {
      name: testWorkspace.name,
      key: testWorkspace.key,
      description: testWorkspace.description,
    });
    workspaceId = created.id;
  });

  test('creates a workspace iteration via the UI', async () => {
    const iter = generateIteration('ws-create');

    await iterationPage.gotoWorkspace(workspaceId);
    await iterationPage.createIteration({
      name: iter.name,
      start_date: iter.start_date,
      end_date: iter.end_date,
      status: 'planned',
    });

    await iterationPage.verifyStatus(iter.name, 'Planned');
  });

  test('creates a global iteration via the UI from /iterations', async () => {
    const iter = generateIteration('global-create');

    await iterationPage.gotoGlobal();
    await iterationPage.createIteration({
      name: iter.name,
      start_date: iter.start_date,
      end_date: iter.end_date,
      status: 'planned',
    });
    await iterationPage.verifyStatus(iter.name, 'Planned');
  });

  test('changes workspace iteration status from planned to active via edit modal', async ({
    request,
  }) => {
    const iter = generateIteration('status');
    await createIterationViaAPI(request, {
      ...iter,
      workspace_id: workspaceId,
      type_id: iterationTypeId,
    });

    await iterationPage.gotoWorkspace(workspaceId);
    await iterationPage.verifyStatus(iter.name, 'Planned');
    await iterationPage.changeStatusViaEdit(iter.name, 'active');
    await iterationPage.verifyStatus(iter.name, 'Active');
  });

  test('deletes a workspace iteration via the row dropdown', async ({ request }) => {
    const iter = generateIteration('delete');
    await createIterationViaAPI(request, {
      ...iter,
      workspace_id: workspaceId,
      type_id: iterationTypeId,
    });

    await iterationPage.gotoWorkspace(workspaceId);
    await iterationPage.verifyIterationExists(iter.name);
    await iterationPage.deleteIteration(iter.name);
  });

  test('assigns an iteration to a work item via the item-detail sidebar', async ({
    page,
    request,
  }) => {
    const iter = generateIteration('assign');
    const created = await createIterationViaAPI(request, {
      ...iter,
      workspace_id: workspaceId,
      type_id: iterationTypeId,
    });
    const itemData = generateItem(0, 'iter-assign');
    await createItemViaAPI(request, workspaceId, { title: itemData.title });

    await itemPage.gotoWorkspaceBacklog(String(workspaceId));
    await itemPage.openItemDetailModal(itemData.title);

    const dialog = page.locator('[role="dialog"]');
    await dialog.locator('[data-testid="iteration-field"]').click();
    await page.locator(`[role="option"][data-option-id="${created.id}"]`).click();
    await expect(dialog.locator('[data-testid="iteration-field"]')).toContainText(iter.name, {
      timeout: 5000,
    });
  });

  test('moves and assigns a work item from the backlog action menu', async ({ page, request }) => {
    const iter = generateIteration('backlog-actions');
    const createdIteration = await createIterationViaAPI(request, {
      ...iter,
      workspace_id: workspaceId,
      type_id: iterationTypeId,
    });
    const firstData = generateItem(0, 'backlog-first');
    const targetData = generateItem(0, 'backlog-target');
    await createItemViaAPI(request, workspaceId, { title: firstData.title });
    const target = await createItemViaAPI(request, workspaceId, { title: targetData.title });

    await itemPage.gotoWorkspaceBacklog(String(workspaceId));

    await page.getByTestId(`backlog-item-menu-${target.id}`).click();
    await page.getByTestId(`backlog-move-start-${target.id}`).click();
    await expect(page.getByTestId('backlog-item').first()).toContainText(targetData.title);

    await page.getByTestId(`backlog-item-menu-${target.id}`).click();
    await page.getByTestId(`backlog-assign-iteration-menu-${target.id}`).click();
    await page.getByTestId(`backlog-assign-iteration-${target.id}-${createdIteration.id}`).click();

    const iterationSection = page.getByTestId(`backlog-iteration-section-${createdIteration.id}`);
    await expect(iterationSection.getByTestId(`backlog-item-menu-${target.id}`)).toBeVisible();
  });

  test('filters items by iteration via a collection using a raw CQL query', async ({
    page,
    request,
  }) => {
    const iter = generateIteration('filter');
    const iteration = await createIterationViaAPI(request, {
      ...iter,
      workspace_id: workspaceId,
      type_id: iterationTypeId,
    });

    const includedItem = generateItem(0, 'in-iter');
    const excludedItem = generateItem(0, 'off-iter');
    const included = await createItemViaAPI(request, workspaceId, { title: includedItem.title });
    await createItemViaAPI(request, workspaceId, { title: excludedItem.title });
    await updateItemViaAPI(request, included.id, { iteration_id: iteration.id });

    const collectionData = generateCollection('by-iteration');
    const collection = await createCollectionViaAPI(request, {
      name: collectionData.name,
      description: collectionData.description,
      ql_query: `iteration_id = ${iteration.id}`,
      workspace_id: workspaceId,
    });

    // The collection-scoped backlog view applies the collection's ql_query
    // server-side. `/workspaces/{id}/collections/{cid}` alone routes to
    // workspace-detail (overview), which doesn't filter.
    await page.goto(`/workspaces/${workspaceId}/collections/${collection.id}/backlog`);
    await page.waitForLoadState('networkidle');

    await expect(page.getByText(includedItem.title).first()).toBeVisible({ timeout: 10000 });
    await expect(page.getByText(excludedItem.title)).toHaveCount(0);
  });
});
