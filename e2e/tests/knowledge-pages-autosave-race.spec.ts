import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { KnowledgePage } from '../pages/knowledge.page';
import { WorkspacePage } from '../pages/workspace.page';

/**
 * Regression: page autosave used to clobber keystrokes typed while a
 * previous save was in flight.
 *
 * Old failure mode: flushSave snapshots draftTitle, awaits the PUT,
 * then unconditionally writes draftTitle = response.title and clears
 * `dirty`. If the user kept typing during the in-flight window, those
 * newer keystrokes were overwritten by the response's older title and
 * `dirty=false` meant no follow-up save ever fired — silent data loss.
 *
 * Fix shape: saveInFlight guards re-entry; the response handler only
 * folds the response back into the draft when the snapshot we sent is
 * still equal to the current draft. If the user typed during the
 * in-flight window, the newer draft is preserved and another autosave
 * is scheduled.
 *
 * This spec slows the first PUT enough to type a second title while
 * the request is hanging, then asserts the final persisted title is
 * the newer one — not the one that was in flight.
 */
test.describe('Knowledge Pages — autosave race', () => {
  let workspaceId: string;

  test.beforeAll(async ({ browser }) => {
    const context = await browser.newContext();
    const setupPage = await context.newPage();
    const workspacePage = new WorkspacePage(setupPage);
    const data = generateWorkspace('knowledge-autosave-race');
    await workspacePage.createWorkspace(data);
    workspaceId = await workspacePage.getWorkspaceId(data.name);
    await context.close();
  });

  test('keystrokes during an in-flight save are not clobbered', async ({ page }) => {
    const knowledge = new KnowledgePage(page);
    const pageId = await knowledge.createRootPage(workspaceId, 'Race-baseline');

    // Slow down the very next PUT to this page so the user can type
    // again before the response returns. Subsequent PUTs pass through
    // immediately so the follow-up autosave finishes in a normal
    // amount of time.
    let putCount = 0;
    const slowPutEndpoint = `/api/workspaces/${workspaceId}/pages/${pageId}`;
    await page.route(`**${slowPutEndpoint}`, async (route, request) => {
      if (request.method() === 'PUT' && putCount === 0) {
        putCount += 1;
        await new Promise((resolve) => setTimeout(resolve, 2500));
      }
      await route.continue();
    });

    // First save: triggers the slow PUT. Don't await waitForAutosave
    // here — we want to keep typing while the request is still hanging.
    await knowledge.titleInput.click();
    await knowledge.titleInput.fill('TitleOne');

    // Wait until the slow PUT is on the wire. The autosave debounces
    // 1.2s after the last input, so a small grace window above that
    // confirms the request has been issued.
    await page.waitForRequest(
      (req) => req.method() === 'PUT' && req.url().endsWith(slowPutEndpoint),
      { timeout: 5000 }
    );
    // Now the save is in flight (saveInFlight=true). Type a newer
    // title — this is the keystroke the old code would have lost.
    await knowledge.titleInput.fill('TitleTwo');

    // Let the slow PUT come back, then the follow-up autosave fires
    // with the newer title. waitForAutosave waits for the next PUT to
    // land and the save-status badge to flip to "saved", which only
    // happens once draftTitle matches the snapshot — i.e. the newer
    // title has been persisted.
    await knowledge.waitForAutosave(workspaceId, pageId);

    // Local state shows the newer title (not "TitleOne" echoing back
    // from the slow response).
    await expect(knowledge.titleInput).toHaveValue('TitleTwo');

    // Server state survives a reload — proves the second save landed.
    await page.unrouteAll({ behavior: 'wait' });
    await page.reload();
    await page.waitForLoadState('networkidle');
    await expect(knowledge.titleInput).toHaveValue('TitleTwo');
  });
});
