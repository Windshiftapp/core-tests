import { createItemViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateItem, generateWorkspace } from '../fixtures/test-data';

/**
 * Command palette → "Recently viewed work items" sub-palette.
 *
 * The launcher is the top-bucket (RECENT) entry, so with an empty query it is
 * the first option when the palette opens. Activating it switches the palette
 * into recent-items mode, populated from GET /api/homepage's recently_viewed
 * list. The view itself is recorded by the activity tracker when an item's
 * detail is opened (GET /api/items/{id}).
 */
test.describe('Command palette: recently viewed', () => {
  let workspaceNumericId: number;
  let itemId: number;
  let itemTitle: string;

  test.beforeAll(async ({ request }) => {
    const ws = await createWorkspaceViaAPI(request, generateWorkspace('cmdpal'));
    workspaceNumericId = ws.id;
    const itemData = generateItem(workspaceNumericId, 'recent');
    itemTitle = itemData.title;
    const item = await createItemViaAPI(request, workspaceNumericId, itemData);
    itemId = item.id;
  });

  async function openPalette(page) {
    // Double-space (within 300ms) opens the palette — see MainApp.svelte's
    // keydown handler. Send the same two keyboard events a user would send.
    const input = page.getByTestId('command-palette-input');
    await expect(async () => {
      await page.keyboard.press('Space');
      await page.keyboard.press('Space');
      await expect(input).toBeVisible({ timeout: 1000 });
    }).toPass({ timeout: 10000 });
  }

  test('launcher is the default first entry and opens the recent-items sub-palette', async ({
    page,
  }) => {
    // Open the item's detail once so it is recorded as recently viewed.
    await page.goto(`/workspaces/${workspaceNumericId}/items/${itemId}`);
    await expect(page.getByTestId('item-title-edit')).toContainText(itemTitle);

    // Back to the homepage, then open the palette with an empty query.
    await page.goto('/');
    await openPalette(page);

    // The recently-viewed launcher is available in the empty command list.
    await expect(page.getByTestId('command-palette-option-recently-viewed')).toBeVisible();

    // Activate it → recent-items sub-palette, showing the viewed item.
    await page.getByTestId('command-palette-option-recently-viewed').click();
    await expect(page.getByTestId('command-palette-recent-back')).toBeVisible();
    const recentItem = page.getByTestId(`command-palette-option-recent-item-${itemId}`);
    await expect(recentItem).toBeVisible();
    await expect(recentItem).toContainText(itemTitle);

    // Selecting it navigates to the item.
    await recentItem.click();
    await expect(page).toHaveURL(new RegExp(`/items/${itemId}(\\b|/|$)`));
  });

  test('back control returns to the command list', async ({ page }) => {
    await page.goto('/');
    await openPalette(page);
    await page.getByTestId('command-palette-option-recently-viewed').click();
    await expect(page.getByTestId('command-palette-recent-back')).toBeVisible();

    await page.getByTestId('command-palette-recent-back').click();
    // Back in command mode the launcher entry is visible again.
    await expect(page.getByTestId('command-palette-option-recently-viewed')).toBeVisible();
    await expect(page.getByTestId('command-palette-recent-back')).toHaveCount(0);
  });
});
