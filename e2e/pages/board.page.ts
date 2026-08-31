import { expect, type Page } from '../fixtures/context-path';

/**
 * Page Object for Board View
 */
export class BoardPage {
  readonly page: Page;

  constructor(page: Page) {
    this.page = page;
  }

  readonly boardView = '[data-testid="board-view"]';
  readonly column = '[data-testid="board-column"]';
  readonly columnHeader = '[data-testid="column-header"]';
  readonly card = '.board-card';
  readonly cardTitle = '.board-card h4';

  /**
   * Navigate to board view for a workspace
   */
  async goto(workspaceKey: string) {
    await this.page.goto(`/workspaces/${workspaceKey}/board`);
    await this.page.waitForLoadState('networkidle');
  }

  /**
   * Verify board view is displayed
   */
  async verifyBoardVisible() {
    const board = this.page.locator(this.boardView);
    await expect(board).toBeVisible({ timeout: 10000 });
  }

  /**
   * Get column count
   */
  async getColumnCount(): Promise<number> {
    await this.page.locator(this.column).first().waitFor({ state: 'visible', timeout: 10000 });
    return this.page.locator(this.column).count();
  }

  /**
   * Get column names
   */
  async getColumnNames(): Promise<string[]> {
    await this.page
      .locator(this.columnHeader)
      .first()
      .waitFor({ state: 'visible', timeout: 10000 });
    const headers = this.page.locator(this.columnHeader);
    const count = await headers.count();
    const names: string[] = [];
    for (let i = 0; i < count; i++) {
      const text = await headers.nth(i).textContent();
      if (text) names.push(text.trim());
    }
    return names;
  }

  /**
   * Get card count in a column
   */
  async getCardCountInColumn(columnName: string): Promise<number> {
    const column = this.page.locator(
      `${this.column}:has(${this.columnHeader}:has-text("${columnName}"))`
    );
    return column.locator(this.card).count();
  }

  /**
   * Find card by title
   */
  async findCardByTitle(title: string) {
    return this.page.locator(`${this.card}:has-text("${title}")`).first();
  }

  /**
   * Click on a card to view details.
   * Targets the h4 title element to avoid DnD library intercepting clicks on the card container.
   */
  async clickCard(title: string) {
    const cardTitle = this.page.locator(`${this.cardTitle}:has-text("${title}")`).first();
    await cardTitle.click();
    await this.page.waitForLoadState('networkidle');
  }

  /**
   * Verify card exists on the board
   */
  async verifyCardExists(title: string) {
    const card = await this.findCardByTitle(title);
    await expect(card).toBeVisible({ timeout: 10000 });
  }

  /**
   * Verify card is in specific column
   */
  async verifyCardInColumn(title: string, columnName: string) {
    const column = this.page.locator(
      `${this.column}:has(${this.columnHeader}:has-text("${columnName}"))`
    );
    const card = column.locator(`${this.card}:has-text("${title}")`);
    await expect(card).toBeVisible({ timeout: 10000 });
  }
}
