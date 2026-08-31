import { expect, externalPath, type Page } from '../fixtures/context-path';

/**
 * Page Object for Admin Panel
 */
export class AdminPage {
  readonly page: Page;

  constructor(page: Page) {
    this.page = page;
  }

  readonly adminSearch = '#admin-search';
  readonly tabContent = 'main';

  /**
   * Navigate to admin panel
   */
  async goto() {
    await this.page.goto('/admin');
    await expect(this.page.locator('#admin-search')).toBeVisible({ timeout: 10000 });
  }

  /**
   * Click a tab/section in the admin sidebar and wait for navigation to the
   * tab's real route. The destination is taken from the link's own href rather
   * than derived from the label — some labels don't map 1:1 to their path
   * (e.g. "AI Connections" → /admin/llm-connections), and asserting against the
   * actual href is what catches a navigation that silently didn't happen.
   * Throws (no swallowed catch) if the link is missing or the URL never lands.
   */
  async clickTab(tabName: string) {
    const tabId = tabName.toLowerCase().replace(/\s+/g, '-');
    let link = this.page.locator(`a[href="${externalPath(`/admin/${tabId}`)}"]`).first();
    if ((await link.count()) === 0 || !(await link.isVisible().catch(() => false))) {
      // Fall back to matching by visible label text within the admin nav.
      link = this.page
        .locator(`nav a[href^="${externalPath('/admin/')}"]`)
        .filter({ hasText: tabName })
        .first();
    }
    const href = await link.getAttribute('href');
    if (!href) {
      throw new Error(`Admin tab "${tabName}" has no sidebar link`);
    }
    await link.click();
    // Strict: the route must actually change to the link's destination.
    await this.page.waitForURL((url) => url.pathname === href, { timeout: 5000 });
    await this.page.waitForLoadState('networkidle', { timeout: 5000 }).catch(() => {});
  }

  /** Assert the browser is on the given admin sub-route. */
  async expectOnTab(tabId: string) {
    await expect(this.page).toHaveURL(new RegExp(`/admin/${tabId}(?:[/?#]|$)`));
  }

  /**
   * Verify admin panel is accessible (content is visible, no login redirect)
   */
  async verifyAdminAccessible() {
    await this.goto();
    const searchInput = this.page.locator(this.adminSearch);
    await expect(searchInput).toBeVisible({ timeout: 5000 });
  }

  /**
   * Verify admin panel is NOT accessible (should see login)
   */
  async verifyAdminNotAccessible() {
    await this.goto();
    // Should see login form (emailOrUsername input)
    const loginInput = this.page.locator('#emailOrUsername');
    await expect(loginInput).toBeVisible({ timeout: 5000 });
  }

  /**
   * Verify tab content is loaded
   */
  async verifyTabContentVisible() {
    const content = this.page.locator(this.tabContent);
    await expect(content.first()).toBeVisible({ timeout: 5000 });
  }

  /**
   * Get visible sidebar navigation items
   */
  async getVisibleTabs(): Promise<string[]> {
    const links = this.page.getByTestId('admin-navigation-item');
    await expect(links.first()).toBeVisible({ timeout: 10000 });
    const count = await links.count();
    const names: string[] = [];
    for (let i = 0; i < count; i++) {
      const text = await links.nth(i).textContent();
      if (text) names.push(text.trim());
    }
    return names;
  }
}
