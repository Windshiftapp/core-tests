import { expect, type Locator, type Page } from '../fixtures/context-path';

/**
 * Page object for the item-linking UI — the "Linked Items" section in the
 * item detail modal plus the LinkItemModal dialog it opens. Assumes the
 * item detail modal is already open before any of these methods are called
 * (e.g. via ItemPage.openItemDetailModal).
 */
export class ItemLinksPage {
  readonly page: Page;

  constructor(page: Page) {
    this.page = page;
  }

  /**
   * Click the "+ Add" button in the Linked Items section of the item detail
   * modal and wait for the link modal to appear.
   */
  async openLinkModal(): Promise<void> {
    // Two "Add link" buttons can coexist: one in the description action bar
    // (always visible) and one in the Linked Items section (only when ≥1
    // link exists). Clicking the first match handles both empty and
    // populated states.
    await this.page.getByTestId('add-link-button').first().click();
    await this.linkModal().waitFor({ state: 'visible', timeout: 5000 });
  }

  /**
   * Root of the LinkItemModal — use as a scope for all nested locators so
   * selectors don't collide with the item detail modal behind it.
   */
  linkModal(): Locator {
    return this.page.getByTestId('link-modal');
  }

  /**
   * Select a link type by visible name (e.g. "Relates To") from the
   * BasePicker. BasePicker renders as a button that opens an overlay; the
   * overlay list items are rendered elsewhere in the DOM, so we don't scope
   * the option click to the modal.
   */
  async selectLinkType(name: string): Promise<void> {
    const picker = this.linkModal().locator('#link-type-picker');
    await picker.click();
    const option = this.page
      .getByRole('option', { name })
      .or(this.page.locator('[role="option"]').filter({ hasText: name }));
    await option.first().click();
    await expect(picker).toHaveValue(name, { timeout: 5000 });
    await expect(this.linkModal().locator('#link-target-search')).toBeEnabled({ timeout: 5000 });
  }

  /**
   * Type into the target-item search input and click the first search
   * result. Waits for at least one result to appear before selecting. The
   * debounce in the modal is ~300ms so `query` needs to be ≥2 chars.
   */
  async searchAndSelect(query: string): Promise<void> {
    const input = this.linkModal().locator('#link-target-search');
    await input.waitFor({ state: 'visible', timeout: 5000 });
    await expect(input).toBeEnabled({ timeout: 5000 });

    // Start waiting before filling so a fast local response cannot race past
    // Playwright's response listener. Results are rendered in a fixed-position
    // dropdown, so use the page-level test id rather than scoping to the modal.
    const responsePromise = this.page
      .waitForResponse(
        (res) => res.request().method() === 'GET' && res.url().includes('/api/links/search'),
        { timeout: 10000 }
      )
      .catch(() => null);

    await input.fill(query);
    await responsePromise;

    const result = this.page.getByTestId('link-search-result').first();
    await expect(result).toBeVisible({ timeout: 10000 });
    await result.click();
  }

  /**
   * Type into the target-item search input and return the count of visible
   * result rows. Waits long enough for the debounce + network round-trip.
   */
  async searchResultCount(query: string): Promise<number> {
    const input = this.linkModal().locator('#link-target-search');
    await expect(input).toBeEnabled({ timeout: 5000 });

    const responsePromise = this.page
      .waitForResponse(
        (res) => res.request().method() === 'GET' && res.url().includes('/api/links/search'),
        { timeout: 10000 }
      )
      .catch(() => null);

    await input.fill(query);
    await responsePromise;

    const results = this.page.getByTestId('link-search-result');
    await expect.poll(async () => results.count(), { timeout: 10000 }).toBeGreaterThan(0);
    return await results.count();
  }

  /**
   * Click the footer submit button. The submit label is "Add Link" (same
   * text as the header), so we scope by role within the modal.
   */
  async submitLink(): Promise<void> {
    await this.linkModal().getByRole('button', { name: 'Add Link' }).click();
    await this.linkModal().waitFor({ state: 'detached', timeout: 5000 });
  }

  /**
   * All link rows currently rendered in the Linked Items section of the
   * item detail modal.
   */
  linkRows(): Locator {
    return this.page.getByTestId('linked-item-row');
  }

  /**
   * Assert that at least one link row contains the given linked item title.
   */
  async expectLinkVisible(title: string): Promise<void> {
    await expect(this.linkRows().filter({ hasText: title }).first()).toBeVisible({
      timeout: 5000,
    });
  }

  /**
   * Assert no link row contains the given linked item title.
   */
  async expectLinkAbsent(title: string): Promise<void> {
    await expect(this.linkRows().filter({ hasText: title })).toHaveCount(0, {
      timeout: 5000,
    });
  }

  /**
   * Hover a link row (so the delete button appears) and click the delete
   * button. Confirms the row is removed afterwards.
   */
  async deleteLink(title: string): Promise<void> {
    const row = this.linkRows().filter({ hasText: title }).first();
    await row.hover();
    await row.getByTestId('linked-item-delete').click();
    await expect(row).toHaveCount(0, { timeout: 5000 });
  }
}
