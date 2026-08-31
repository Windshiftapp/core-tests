import { expect, type Page } from '../fixtures/context-path';

/**
 * Page Object for List View
 */
export class ListViewPage {
  readonly page: Page;

  constructor(page: Page) {
    this.page = page;
  }

  readonly listView = '[data-testid="list-view"]';
  readonly tableHeader = '[data-testid="list-header"] button, [data-testid="list-header"] div';
  readonly tableRow = '[data-item-row]';
  readonly sortHeader = '[data-testid="list-header"] button';

  /**
   * Navigate to list view for a workspace
   */
  async goto(workspaceKey: string) {
    await this.page.goto(`/workspaces/${workspaceKey}/list`);
    await this.page.waitForLoadState('networkidle');
  }

  /**
   * Verify list view is displayed
   */
  async verifyListVisible() {
    const list = this.page.locator(this.listView);
    await expect(list).toBeVisible({ timeout: 10000 });
  }

  /**
   * Get row count
   */
  async getRowCount(): Promise<number> {
    return this.page.locator(this.tableRow).count();
  }

  /**
   * Find row by item title
   */
  async findRowByTitle(title: string) {
    return this.page.locator(`${this.tableRow}:has-text("${title}")`).first();
  }

  /**
   * Click on a row to view item details
   */
  async clickRow(title: string) {
    const row = await this.findRowByTitle(title);
    // Click the item key link within the row (title cells are inline-editable, not links)
    const keyLink = row.locator('a').first();
    await keyLink.click();
    await this.page.waitForLoadState('networkidle');
  }

  /**
   * Verify item exists in list
   */
  async verifyItemInList(title: string) {
    const row = await this.findRowByTitle(title);
    await expect(row).toBeVisible({ timeout: 10000 });
  }

  /**
   * Sort by column header
   */
  async sortByColumn(columnName: string) {
    const header = this.page
      .locator(`[data-testid="list-header"] button:has-text("${columnName}")`)
      .first();
    await header.click();
    // Sort re-fetches the list; wait for the network to settle
    await this.page.waitForLoadState('networkidle', { timeout: 5000 }).catch(() => {});
  }

  /**
   * Read the visible rows in their current DOM order. Each entry is the row's
   * full text, which includes the item title — enough to assert sort order.
   */
  async getRowTexts(): Promise<string[]> {
    return this.page.locator(this.tableRow).allInnerTexts();
  }

  /**
   * Index of the first row whose text contains `title`, or -1 if absent.
   * Used to assert relative ordering after a sort.
   */
  async rowIndexOf(title: string): Promise<number> {
    const texts = await this.getRowTexts();
    return texts.findIndex((text) => text.includes(title));
  }

  /**
   * Get column headers
   */
  async getColumnHeaders(): Promise<string[]> {
    const headers = this.page.locator(this.tableHeader);
    const count = await headers.count();
    const names: string[] = [];
    for (let i = 0; i < count; i++) {
      const text = await headers.nth(i).textContent();
      if (text) names.push(text.trim());
    }
    return names;
  }
}
