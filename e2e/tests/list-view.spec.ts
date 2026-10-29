import { expect, test } from '../fixtures/context-path';
import { authenticateAdminRequest, createItemViaAPI } from '../fixtures/api-helpers';
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

    test('keeps the title readable when fixed columns are sized wide', async ({
      page,
      request,
    }) => {
      await authenticateAdminRequest(request);

      // Several XL fixed columns would otherwise consume the whole row and
      // collapse the flexible Title track until its text spills over the
      // Status cell.
      const configResponse = await request.put(
        `/api/v2/workspaces/${workspaceId}/board-configuration`,
        {
          headers: { 'Sec-Fetch-Site': 'same-origin' },
          data: {
            columns: [],
            backlog_status_ids: [],
            list_columns: [
              { field_identifier: 'key', field_type: 'system', display_order: 0, width: 1 },
              { field_identifier: 'title', field_type: 'system', display_order: 1, width: 4 },
              { field_identifier: 'status', field_type: 'system', display_order: 2, width: 4 },
              { field_identifier: 'priority', field_type: 'system', display_order: 3, width: 4 },
              { field_identifier: 'created_at', field_type: 'system', display_order: 4, width: 4 },
              { field_identifier: 'milestone', field_type: 'system', display_order: 5, width: 4 },
            ],
            card_fields: [],
          },
        }
      );
      expect(configResponse.ok(), `list config save failed (${configResponse.status()})`).toBeTruthy();

      const item = await createItemViaAPI(request, Number(workspaceId), {
        title: 'Design levels',
      });

      await page.setViewportSize({ width: 900, height: 800 });
      await listViewPage.goto(workspaceId);

      const titleCell = page.getByTestId(`workspace-item-title-${item.id}`);
      await titleCell.waitFor({ state: 'attached' });

      const metrics = await titleCell.evaluate((el) => ({
        scrollWidth: el.scrollWidth,
        clientWidth: el.clientWidth,
      }));
      expect(metrics.clientWidth, 'Title column must keep a readable width').toBeGreaterThan(150);
      expect(metrics.scrollWidth, 'Title text must stay inside its own cell').toBeLessThanOrEqual(
        metrics.clientWidth + 1
      );
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
        .poll(async () => {
          const a = await listViewPage.rowIndexOf(itemA.title);
          const b = await listViewPage.rowIndexOf(itemB.title);
          return a >= 0 && b >= 0 && a < b;
        }, { message: 'ascending Title sort should place alpha before beta', timeout: 10000 })
        .toBe(true);

      // Second click → descending: order must flip to beta before alpha.
      await listViewPage.sortByColumn('Title');
      await expect
        .poll(async () => {
          const a = await listViewPage.rowIndexOf(itemA.title);
          const b = await listViewPage.rowIndexOf(itemB.title);
          return a >= 0 && b >= 0 && b < a;
        }, { message: 'descending Title sort should place beta before alpha', timeout: 10000 })
        .toBe(true);
    });
  });

  test.describe('List Navigation', () => {
    test('opens the item detail modal when clicking the item key', async ({ request }) => {
      const item = generateItem(0, 'click-row');

      await createItemViaAPI(request, Number(workspaceId), {
        title: item.title,
        description: item.description,
      });

      await listViewPage.goto(workspaceId);
      await listViewPage.clickRow(item.title);

      // Item keys keep a real href for new tabs but open the global modal on a
      // plain left click, so the URL stays on the list view.
      const modal = listViewPage.page.getByTestId('item-detail-modal');
      await expect(modal).toBeVisible({ timeout: 10000 });
      await expect(modal.getByTestId('item-title-edit')).toHaveText(item.title);
    });
  });
});
