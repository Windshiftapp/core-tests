import { createItemViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateItem, generateWorkspace } from '../fixtures/test-data';

/**
 * Mobile mention-picker regression (WI-431).
 *
 * On phones the on-screen keyboard (Gboard, iOS) routinely does NOT emit
 * keydown/keyup events for character input — it dispatches an `input`
 * (beforeinput/insertText) event that ProseMirror applies directly. The
 * MilkdownEditor used to detect the `@` mention trigger only on `keyup`, so on
 * mobile the trigger never fired and the picker never opened — you could type
 * `@name` but no picker appeared and no user could be selected.
 *
 * Playwright's `keyboard.insertText` reproduces exactly this path: a single
 * input event, no keystrokes. (The sibling rich-editor spec relies on this
 * same property — see its comment on insertText.) So `insertText('@')` opening
 * the picker is the proof the trigger no longer depends on keyup.
 *
 * The fix drives mention detection off Milkdown's `listener.updated` callback,
 * which fires on every document change regardless of input method.
 */
test.describe('Mention picker — mobile / IME input (WI-431)', () => {
  test('@ inserted via input event (no keyup) opens the picker', async ({ page, request }) => {
    test.setTimeout(60_000);

    const suffix = `mob${Date.now()}`;
    const ws = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
    const itemData = generateItem(ws.id, suffix);
    const item = await createItemViaAPI(request, ws.id, {
      title: itemData.title,
      description: itemData.description,
    });

    await page.goto(`/workspaces/${ws.id}/items/${item.id}`);
    await expect(page.locator('[data-testid="comments-section"]')).toBeVisible({ timeout: 15_000 });

    const composer = page.getByTestId('comment-composer');
    await expect(composer).toHaveAttribute('data-ready', 'true', {
      timeout: 15_000,
    });
    await composer.click();

    // The mobile path: a pure input event, no keydown/keyup. Before the fix
    // this never reached checkForMentionTrigger and the picker stayed closed.
    await page.keyboard.insertText('@');

    const picker = page.getByTestId('mention-picker');
    await expect(picker).toBeVisible({ timeout: 5000 });

    // And it must list selectable users (the @ trigger + /users query wired
    // up), so a mention can actually be completed on mobile.
    await expect(picker.getByTestId('mention-option').first()).toBeVisible({
      timeout: 5000,
    });
  });

  // The mobile item-detail container (.detail) used to carry a persistent
  // `will-change: transform` (for pull-to-refresh). That makes it a containing
  // block for position:fixed descendants — including the mention picker — so
  // the picker's viewport coords get reinterpreted relative to the scrolled
  // container and it lands off-screen by the scroll distance. It only showed up
  // on tall items (commits/PR + coding-agent panels, long descriptions) where
  // you must scroll down to reach the composer. The picker must stay in the
  // viewport regardless of scroll.
  test('picker stays in the viewport on a tall, scrolled mobile item', async ({
    page,
    request,
  }) => {
    test.setTimeout(60_000);
    await page.setViewportSize({ width: 390, height: 844 });

    const suffix = `tall${Date.now()}`;
    const ws = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
    const itemData = generateItem(ws.id, suffix);
    // A long description forces the comment composer well below the fold, so
    // reaching it requires a large scroll — that scroll is exactly what the
    // containing-block bug offsets the fixed picker by.
    const longDescription = Array.from(
      { length: 60 },
      (_, i) => `Line ${i + 1}: padding so the composer sits far below the fold.`
    ).join('\n\n');
    const item = await createItemViaAPI(request, ws.id, {
      title: itemData.title,
      description: longDescription,
    });

    // The mobile PWA surface is route-gated (/m/items/:id), not viewport-gated.
    await page.goto(`/m/items/${item.id}`);
    await expect(page.locator('[data-testid="mobile-item-detail"]')).toBeVisible({
      timeout: 15_000,
    });

    const composer = page.getByTestId('comment-composer');
    await expect(composer).toHaveAttribute('data-ready', 'true', {
      timeout: 15_000,
    });
    // Scrolling the composer into view puts the page in the scrolled state that
    // triggered the off-screen placement.
    await composer.scrollIntoViewIfNeeded();
    await composer.click();

    await page.keyboard.insertText('@');

    const picker = page.getByTestId('mention-picker');
    await expect(picker).toBeVisible({ timeout: 5000 });
    // The real assertion: the picker is actually on the screen, not pushed off
    // by the scroll offset. Without the fix it renders far outside the viewport.
    await expect(picker).toBeInViewport();
  });
});
