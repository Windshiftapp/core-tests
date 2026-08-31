import { createItemViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateItem, generateWorkspace } from '../fixtures/test-data';
import { ListViewPage } from '../pages/list-view.page';
import { WorkspacePage } from '../pages/workspace.page';

/**
 * List View Tests
 * Tests list view display, sorting, and navigation to detail
 */

test.describe('List View', () => {
  let listViewPage: ListViewPage;
  let workspacePage: WorkspacePage;
  let testWorkspace: ReturnType<typeof generateWorkspace>;
  let workspaceId: string;

  test.beforeEach(async ({ page }) => {
    listViewPage = new ListViewPage(page);
    workspacePage = new WorkspacePage(page);
    testWorkspace = generateWorkspace();

    // Create workspace
    await workspacePage.createWorkspace(testWorkspace);
    workspaceId = await workspacePage.getWorkspaceId(testWorkspace.name);
  });

  test.describe('List Display', () => {
    test('should display list view', async () => {
      await listViewPage.goto(workspaceId);
      await listViewPage.verifyListVisible();
    });

    test('should display column headers', async ({ request }) => {
      // Create an item so the table (with headers) renders instead of empty state
      const item = generateItem(0, 'headers-test');
      await createItemViaAPI(request, Number(workspaceId), { title: item.title });

      await listViewPage.goto(workspaceId);

      const headers = await listViewPage.getColumnHeaders();
      expect(headers.length).toBeGreaterThan(0);
    });

    test('should display items in list', async ({ request }) => {
      const item = generateItem(0, 'list-item');

      await createItemViaAPI(request, Number(workspaceId), {
        title: item.title,
        description: item.description,
      });

      await listViewPage.goto(workspaceId);
      await listViewPage.verifyItemInList(item.title);
    });

    test('should display multiple items', async ({ request }) => {
      const item1 = generateItem(0, 'list1');
      const item2 = generateItem(0, 'list2');
      const item3 = generateItem(0, 'list3');

      await createItemViaAPI(request, Number(workspaceId), { title: item1.title });
      await createItemViaAPI(request, Number(workspaceId), { title: item2.title });
      await createItemViaAPI(request, Number(workspaceId), { title: item3.title });

      await listViewPage.goto(workspaceId);

      await listViewPage.verifyItemInList(item1.title);
      await listViewPage.verifyItemInList(item2.title);
      await listViewPage.verifyItemInList(item3.title);

      const count = await listViewPage.getRowCount();
      expect(count).toBeGreaterThanOrEqual(3);
    });
  });

  test.describe('List Sorting', () => {
    test('should sort items by column', async ({ request }) => {
      // Titles share the "E2E Test Item " prefix, so ascending order is decided
      // by the suffix: "alpha…" sorts before "beta…". Create beta first so the
      // ascending sort has to actually reorder the rows (not just preserve
      // insertion order).
      const itemA = generateItem(0, 'alpha');
      const itemB = generateItem(0, 'beta');

      await createItemViaAPI(request, Number(workspaceId), { title: itemB.title });
      await createItemViaAPI(request, Number(workspaceId), { title: itemA.title });

      await listViewPage.goto(workspaceId);

      // The Title column must be sortable — fail loudly if the header is gone
      // rather than silently skipping the assertion.
      const headers = await listViewPage.getColumnHeaders();
      expect(
        headers.some((h) => h.toLowerCase().includes('title') || h.toLowerCase().includes('name')),
        `expected a sortable Title/Name header, got: ${headers.join(', ')}`
      ).toBeTruthy();

      // First click → ascending: alpha must come before beta. The sort
      // re-fetches asynchronously, so poll until the row order settles rather
      // than reading once and racing the re-render.
      await listViewPage.sortByColumn('Title');
      await expect
        .poll(
          async () => {
            const a = await listViewPage.rowIndexOf(itemA.title);
            const b = await listViewPage.rowIndexOf(itemB.title);
            return a >= 0 && b >= 0 && a < b;
          },
          { message: 'ascending Title sort should place alpha before beta', timeout: 10000 }
        )
        .toBe(true);

      // Second click → descending: order must flip to beta before alpha.
      await listViewPage.sortByColumn('Title');
      await expect
        .poll(
          async () => {
            const a = await listViewPage.rowIndexOf(itemA.title);
            const b = await listViewPage.rowIndexOf(itemB.title);
            return a >= 0 && b >= 0 && b < a;
          },
          { message: 'descending Title sort should place beta before alpha', timeout: 10000 }
        )
        .toBe(true);
    });
  });

  test.describe('List Navigation', () => {
    test('should navigate to item detail when clicking row', async ({ request }) => {
      const item = generateItem(0, 'click-row');

      await createItemViaAPI(request, Number(workspaceId), {
        title: item.title,
        description: item.description,
      });

      await listViewPage.goto(workspaceId);
      await listViewPage.clickRow(item.title);

      // Should navigate to item detail page
      await expect(listViewPage.page).toHaveURL(/\/items\/\d+/, { timeout: 10000 });
    });
  });
});
