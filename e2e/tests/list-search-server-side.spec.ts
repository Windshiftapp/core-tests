import { expect, test } from '../fixtures/context-path';
import { createItemViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { generateWorkspace } from '../fixtures/test-data';

/**
 * GH #328: the list view searched only the items already fetched for the
 * current page, so an item on page 2 could not be found by title or key.
 * The search now runs server-side (shared with the board) and the Filter
 * picker offers the CQL `key` field.
 */

const FILLER_ITEMS = 55;
const PAGE_SIZE = 50;

test.describe('List search across pages', () => {
  test('finds an item beyond page 1 by key, title, and the Key filter', async ({
    page,
    request,
  }) => {
    const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
    const workspace = await createWorkspaceViaAPI(
      request,
      generateWorkspace(`list-search-${stamp}`)
    );

    // Fill page 1 first so the needle (created last, highest rank) lands on a
    // later page.
    for (let batch = 0; batch < FILLER_ITEMS; batch += 10) {
      await Promise.all(
        Array.from({ length: Math.min(10, FILLER_ITEMS - batch) }, (_, i) =>
          createItemViaAPI(request, workspace.id, {
            title: `Filler item ${batch + i + 1} ${stamp}`,
          })
        )
      );
    }

    const needleTitle = `Hidden search needle ${stamp}`;
    const needle = await createItemViaAPI(request, workspace.id, {
      title: needleTitle,
      description: `Description-only needle ${stamp}`,
    });
    const needleKey = `${workspace.key}-${needle.workspace_item_number}`;

    await page.goto(`/workspaces/${workspace.id}/list`);
    const needleRow = page.getByTestId(`workspace-item-row-${needle.id}`);
    await expect(page.getByTestId('list-view')).toBeVisible();

    // The premise of the regression: the needle is not on the loaded page.
    await expect(page.getByTestId(/^workspace-item-row-/)).toHaveCount(PAGE_SIZE);
    await expect(needleRow).toHaveCount(0);

    const searchInput = page.getByTestId('list-search-input');
    const keySearch = page.waitForRequest((candidate) => {
      if (!candidate.url().includes('/api/v2/items?')) return false;
      return new URL(candidate.url()).searchParams.get('search') === needleKey;
    });
    await searchInput.fill(needleKey);
    await keySearch;
    await expect(needleRow).toBeVisible();

    // Clearing restores the page-1 window.
    await searchInput.fill('');
    await expect(needleRow).toHaveCount(0);

    const titleSearch = page.waitForRequest((candidate) => {
      if (!candidate.url().includes('/api/v2/items?')) return false;
      return new URL(candidate.url()).searchParams.get('search') === needleTitle;
    });
    await searchInput.fill(needleTitle);
    await titleSearch;
    await expect(needleRow).toBeVisible();

    await searchInput.fill('');
    await expect(needleRow).toHaveCount(0);

    // The Filter picker must offer Key (it exists in the backend QL catalog).
    await page.getByTestId('dynamic-filter-toggle').click();
    await page.getByTestId('filter-menu-add').click();
    await page.getByTestId('field-selector-trigger').click();
    await expect(page.getByTestId('field-option-key')).toBeVisible();
    await page.getByTestId('field-option-key').click();
    await page.getByTestId('collection-dynamic-filter-0-value').click();
    await page.getByTestId('collection-dynamic-filter-0-value-input').fill(needleKey);
    await page.getByTestId('collection-dynamic-filter-0-apply-value').click();

    await expect(needleRow).toBeVisible();
  });
});
