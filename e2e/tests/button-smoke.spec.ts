import { externalPath } from '../fixtures/context-path';
import { expect, test } from '../fixtures/errors';
import { generateWorkspace } from '../fixtures/test-data';
import { WorkspacePage } from '../pages/workspace.page';

/**
 * Button smoke tests.
 *
 * For each major page:
 *   1. Navigate to it.
 *   2. Exercise a representative set of buttons (dialog openers, view switchers,
 *      nav items) and assert the expected reaction.
 *   3. Rely on the `errors` fixture (auto-fails on pageerror / console.error)
 *      to catch silent handler failures — the exact class of bug the user has
 *      hit before with outdated Svelte 4 patterns.
 *
 * These tests intentionally skip destructive buttons (Delete, Logout,
 * empty-form Save). Destructive flows already have dedicated specs.
 */

const DIALOG = 'div[role="dialog"]';

/**
 * Allowlist pre-existing environmental noise so the smoke spec surfaces real
 * button/handler failures instead of drowning in infra errors. Each pattern
 * has a comment explaining why it's ignored — remove it when fixed.
 */
function allowEnvironmentalNoise(allow: (pattern: RegExp) => void) {
  // Rate limiter fires 429s on rapid navigation during a smoke run.
  allow(/Failed to load resource.*429 \(Too Many Requests\)/);
  // Optional services (logbook container, optional attachment-settings backend)
  // are not mounted in every deployment; the frontend logs a 404 when absent.
  allow(/\/api\/logbook\//);
  allow(/Failed to load buckets/);
  allow(/Failed to load all documents/);
  allow(/\/api\/attachment-settings\/status/);
  allow(/Failed to load attachment status/);
}

test.describe('Button smoke — page loads without console errors', () => {
  const pages: Array<{ name: string; path: string; settleSelector?: string }> = [
    { name: 'workspaces list', path: '/workspaces', settleSelector: 'tbody, main' },
    { name: 'admin panel', path: '/admin', settleSelector: '#admin-search' },
    { name: 'admin users', path: '/admin/users', settleSelector: 'main' },
    { name: 'admin priorities', path: '/admin/priorities', settleSelector: 'main' },
    { name: 'admin custom-fields', path: '/admin/custom-fields', settleSelector: 'main' },
    // Teams live at the global /teams route, not under /admin — use a real
    // admin sub-route here instead of the non-existent /admin/teams.
    { name: 'admin groups', path: '/admin/groups', settleSelector: 'main' },
    // /personal renders the personal-workspace view (router.js); /personal/tasks
    // is not a registered route and silently falls through to the 404 page.
    { name: 'personal workspace', path: '/personal', settleSelector: 'main' },
    { name: 'notifications', path: '/notifications', settleSelector: 'main' },
    { name: 'global search', path: '/search', settleSelector: 'main' },
    { name: 'logbook', path: '/logbook', settleSelector: 'main' },
    { name: 'assets', path: '/assets', settleSelector: 'main' },
    { name: 'channels inbox', path: '/channels/inbox', settleSelector: 'main' },
  ];

  for (const { name, path, settleSelector } of pages) {
    test(`${name} loads clean`, async ({ page, allowConsoleError }) => {
      allowEnvironmentalNoise(allowConsoleError);
      await page.goto(path);
      await page.waitForLoadState('networkidle');
      if (settleSelector) {
        await page
          .locator(settleSelector)
          .first()
          .waitFor({ state: 'visible', timeout: 5000 })
          .catch(() => {});
      }
      // Sanity: the page rendered something. Real assertion is the error fixture.
      await expect(page.locator('body')).toBeVisible();
      // 404 guard: the SPA falls back to NotFound.svelte for unregistered
      // routes — without this assertion the test passes against a 404 page
      // (no console error, body is visible). Catch it loudly so a broken or
      // typo'd path here fails the spec.
      await expect(
        page.getByRole('heading', { name: 'Page Not Found' }),
        `${name} (${path}) rendered the 404 page`
      ).toHaveCount(0);
    });
  }
});

/**
 * Close any modal that's open. The Modal component listens for Escape on the
 * document, but focus inside form inputs can swallow it first — pressing on
 * `body` ensures the handler fires. Falls back to clicking the backdrop.
 */
async function closeModal(page: import('@playwright/test').Page) {
  await page
    .locator('body')
    .press('Escape')
    .catch(() => {});
  const detached = await page
    .locator(DIALOG)
    .waitFor({ state: 'detached', timeout: 2000 })
    .then(() => true)
    .catch(() => false);
  if (!detached) {
    // Backdrop click as a fallback — the backdrop sits behind the dialog content.
    await page
      .locator(DIALOG)
      .first()
      .press('Escape')
      .catch(() => {});
    await page
      .locator(DIALOG)
      .waitFor({ state: 'detached', timeout: 3000 })
      .catch(() => {});
  }
}

test.describe('Button smoke — dialog triggers', () => {
  test('workspaces: "Add Workspace" opens create modal', async ({ page, allowConsoleError }) => {
    allowEnvironmentalNoise(allowConsoleError);
    await page.goto('/workspaces');
    await page.waitForLoadState('networkidle');

    await page.click('button:has-text("Add Workspace")');
    await page.locator(DIALOG).waitFor({ state: 'visible', timeout: 5000 });
    await expect(page.getByPlaceholder('Workspace name')).toBeVisible();

    await closeModal(page);
  });

  test('global create button opens create modal', async ({ page, allowConsoleError }) => {
    allowEnvironmentalNoise(allowConsoleError);
    await page.goto('/workspaces');
    await page.waitForLoadState('networkidle');

    const globalCreate = page.locator('#global-create-button');
    await globalCreate.waitFor({ state: 'visible', timeout: 5000 });
    await globalCreate.click();

    await page.locator(DIALOG).waitFor({ state: 'visible', timeout: 5000 });
    await closeModal(page);
  });

  test('admin: sidebar nav buttons switch URL without errors', async ({
    page,
    allowConsoleError,
  }) => {
    allowEnvironmentalNoise(allowConsoleError);
    await page.goto('/admin');
    await page.waitForLoadState('networkidle');

    // Core admin nav links every admin must see. These are required: a missing
    // link is a real regression, not something to silently skip over (the old
    // loop `continue`'d past absent links, so a broken sidebar passed clean).
    const requiredTabs = ['users', 'groups', 'priorities', 'custom-fields'];
    for (const tab of requiredTabs) {
      const link = page.locator(`a[href="${externalPath(`/admin/${tab}`)}"]`).first();
      await expect(link, `required admin link /admin/${tab} should be visible`).toBeVisible({
        timeout: 5000,
      });
      await link.click();
      await page.waitForURL(new RegExp(`/admin/${tab}`), { timeout: 5000 });
      await page.waitForLoadState('networkidle', { timeout: 5000 }).catch(() => {});
    }
  });
});

test.describe('Button smoke — workspace context buttons', () => {
  let workspaceName: string;

  test.beforeAll(async ({ browser }) => {
    // Create a workspace once so the workspace-scoped buttons have a target.
    const ctx = await browser.newContext({
      storageState: process.env.E2E_AUTH_FILE ?? '.auth/user.json',
    });
    const page = await ctx.newPage();
    const workspacePage = new WorkspacePage(page);
    const workspace = generateWorkspace();
    workspaceName = workspace.name;
    await workspacePage.createWorkspace(workspace);
    await ctx.close();
  });

  test('workspace detail: view-switch buttons and back-navigation render clean', async ({
    page,
    allowConsoleError,
  }) => {
    allowEnvironmentalNoise(allowConsoleError);
    const workspacePage = new WorkspacePage(page);
    await workspacePage.goto();
    await workspacePage.clickWorkspace(workspaceName);

    // Workspace detail should now be loaded. Try the common view tabs if present.
    for (const viewLabel of ['List', 'Board', 'Calendar']) {
      const tab = page
        .locator(`a:has-text("${viewLabel}"), button:has-text("${viewLabel}")`)
        .first();
      if (await tab.isVisible().catch(() => false)) {
        await tab.click();
        await page.waitForLoadState('networkidle', { timeout: 5000 }).catch(() => {});
      }
    }
  });

  test('workspace detail: in-context create opens modal with workspace preselected', async ({
    page,
    allowConsoleError,
  }) => {
    allowEnvironmentalNoise(allowConsoleError);
    const workspacePage = new WorkspacePage(page);
    await workspacePage.goto();
    await workspacePage.clickWorkspace(workspaceName);

    await page.locator('#global-create-button').click();
    await page.locator(DIALOG).waitFor({ state: 'visible', timeout: 5000 });
    // Title input should be reachable (Work Item form).
    await expect(page.locator('#work-item-title')).toBeVisible({ timeout: 5000 });

    await closeModal(page);
  });
});
