import { expect, type Page } from '../fixtures/context-path';

/**
 * Page Object for Weekly Calendar View
 */
export class CalendarPage {
  readonly page: Page;

  constructor(page: Page) {
    this.page = page;
  }

  readonly calendarView = '[data-testid="calendar-view"]';
  readonly prevWeekButton = '[data-testid="prev-week"]';
  readonly nextWeekButton = '[data-testid="next-week"]';
  readonly thisWeekButton = '[data-testid="this-week"]';
  readonly calendarItem = '.calendar-time-item';

  /**
   * Navigate to the weekly calendar view.
   *
   * The product only exposes calendar from the personal-workspace sidebar
   * (`WorkspaceNavigation.svelte:401`) at `/personal/calendar`; the
   * `/workspaces/:id/calendar` route is registered but unreachable from
   * any UI surface, so tests should not target it.
   */
  async goto() {
    await this.page.goto('/personal/calendar');
    await this.page.waitForLoadState('networkidle');
  }

  /**
   * Verify calendar is displayed
   */
  async verifyCalendarVisible() {
    const calendar = this.page.locator(this.calendarView);
    await expect(calendar).toBeVisible({ timeout: 10000 });
  }

  /**
   * Get current week range text from the PageHeader subtitle span.
   * The subtitle renders as a <span> inside the header, e.g. "Apr 14 – Apr 20, 2026"
   */
  async getCurrentWeekRange(): Promise<string> {
    const subtitle = this.page
      .locator(`${this.calendarView} [data-testid="page-header-subtitle"]`)
      .first();
    return (await subtitle.textContent()) || '';
  }

  /**
   * Navigate to previous week
   */
  async goToPreviousWeek() {
    const currentRange = await this.getCurrentWeekRange();
    await this.page.click(this.prevWeekButton);
    await this.waitForWeekRangeChange(currentRange);
  }

  /**
   * Navigate to next week
   */
  async goToNextWeek() {
    const currentRange = await this.getCurrentWeekRange();
    await this.page.click(this.nextWeekButton);
    await this.waitForWeekRangeChange(currentRange);
  }

  /**
   * Navigate to this week
   */
  async goToThisWeek() {
    const currentRange = await this.getCurrentWeekRange();
    await this.page.click(this.thisWeekButton);
    // If we're already on "this week" the subtitle won't change, so also allow
    // the network to settle as a fallback.
    await Promise.race([
      this.waitForWeekRangeChange(currentRange),
      this.page.waitForLoadState('networkidle', { timeout: 5000 }).catch(() => {}),
    ]);
  }

  /**
   * Wait for the week range subtitle to change from the previous value.
   */
  private async waitForWeekRangeChange(previous: string) {
    const subtitle = this.page
      .locator(`${this.calendarView} [data-testid="page-header-subtitle"]`)
      .first();
    await expect(subtitle).not.toHaveText(previous, { timeout: 5000 });
  }

  /**
   * Find item on calendar by title
   */
  async findItemByTitle(title: string) {
    return this.page.locator(`${this.calendarItem}:has-text("${title}")`).first();
  }

  /**
   * Verify item is displayed on calendar
   */
  async verifyItemExists(title: string) {
    const item = await this.findItemByTitle(title);
    await expect(item).toBeVisible({ timeout: 10000 });
  }

  /**
   * Click on an item to view details
   */
  async clickItem(title: string) {
    const item = await this.findItemByTitle(title);
    await item.click();
    await this.page.waitForLoadState('networkidle');
  }

  /**
   * Get item count on current view
   */
  async getItemCount(): Promise<number> {
    return this.page.locator(this.calendarItem).count();
  }
}
