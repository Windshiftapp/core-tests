import { expect, type Locator, type Page } from '../fixtures/context-path';

/**
 * Page Object for the workspace Knowledge Pages (wiki) view.
 *
 * The sidebar is now a pages-focused nav (PagesNavSidebar). Per-page
 * actions live behind a `...` kebab on each tree row AND on the
 * right-pane toolbar. The new-page input form was removed — `+` in
 * the sidebar header creates a root Untitled page and focuses the
 * title input; child pages are created from the row kebab. Edits
 * auto-save on a short debounce; there is no Save button.
 *
 * Selectors line up with the stable `id` and `data-testid` attributes:
 *   - `#pages-add-button` — sidebar header + button
 *   - `#page-title-input`
 *   - `[data-testid="page-toolbar-kebab"]` — right-pane ... menu
 *   - `[data-testid="page-save-status"]` — autosave status badge
 *   - `[data-testid="page-tree-item-<id>"]`
 *   - `[data-testid="pages-back-button"]` — drilldown back to workspace
 *   - `[data-testid="dialog-confirm"]` on the global ConfirmDialog
 *   - `[data-testid="page-move-confirm"]` on the move dialog
 */
export class KnowledgePage {
  readonly page: Page;
  readonly addButton: Locator;
  readonly backButton: Locator;
  readonly titleInput: Locator;
  readonly toolbarKebab: Locator;
  readonly saveStatus: Locator;
  readonly tree: Locator;
  readonly editor: Locator;
  readonly toc: Locator;
  readonly tocEntries: Locator;
  readonly modeEditButton: Locator;
  readonly modeReadButton: Locator;

  constructor(page: Page) {
    this.page = page;
    this.addButton = page.locator('#pages-add-button');
    this.backButton = page.locator('[data-testid="pages-back-button"]');
    this.titleInput = page.locator('#page-title-input');
    this.toolbarKebab = page.locator('[data-testid="page-toolbar-kebab"]');
    this.saveStatus = page.locator('[data-testid="page-save-status"]');
    this.tree = page.locator('[data-testid="page-tree"]');
    this.editor = page.locator('[data-testid="page-editor"] .ProseMirror');
    this.toc = page.locator('[data-testid="page-toc"]');
    this.tocEntries = page.locator('[data-testid="page-toc-entry"]');
    this.modeEditButton = page.locator('[data-testid="page-mode-edit"]');
    this.modeReadButton = page.locator('[data-testid="page-mode-read"]');
  }

  /** Flip the editor into the read-only renderer; required to see the TOC. */
  async switchToReadMode() {
    await this.modeReadButton.click();
    await expect(this.modeReadButton).toHaveAttribute('aria-pressed', 'true', {
      timeout: 2000,
    });
  }

  async switchToEditMode() {
    await this.modeEditButton.click();
    await expect(this.modeEditButton).toHaveAttribute('aria-pressed', 'true', {
      timeout: 2000,
    });
  }

  async gotoIndex(workspaceId: string) {
    await this.page.goto(`/workspaces/${workspaceId}/pages`);
    await this.page.waitForLoadState('networkidle');
  }

  async gotoPage(workspaceId: string, pageId: number | string) {
    await this.page.goto(`/workspaces/${workspaceId}/pages/${pageId}`);
    await this.page.waitForLoadState('networkidle');
  }

  /**
   * Create a root page. Navigates to the bare /pages route first so the
   * header `+` path is exercised from the same starting point in every
   * test.
   *
   * Title is supplied by typing it into the focused title input after
   * the sidebar creates an "Untitled" placeholder and PagesView focuses
   * the title field. We save after typing so the test asserts a real
   * persisted title.
   */
  async createRootPage(workspaceId: string, title: string): Promise<number> {
    await this.gotoIndex(workspaceId);
    return this.createPage(workspaceId, title, async () => {
      await this.addButton.click();
    });
  }

  /**
   * Create a child page beneath whichever page is currently selected.
   * Children are created via the selected row's kebab "Add child" action;
   * the sidebar header `+` intentionally always creates root pages.
   */
  async createChildPage(workspaceId: string, title: string): Promise<number> {
    const parentId = this.getCurrentPageId();
    return this.createPage(workspaceId, title, async () => {
      await this.treeItem(parentId).locator('[data-testid="page-kebab"]').click();
      await this.page.locator('[data-menu-item]', { hasText: 'Add child' }).first().click();
    });
  }

  /**
   * Internal: triggers page creation, captures the new page id from the
   * POST response, waits for the route to settle, then replaces the
   * placeholder title with the requested one and saves.
   *
   * Reading the id from the response body rather than the URL avoids a
   * subtle race where waitForURL(/\/pages\/(\d+)/) is satisfied by the
   * PREVIOUS page's URL.
   */
  private async createPage(
    workspaceId: string,
    title: string,
    triggerCreate: () => Promise<void>
  ): Promise<number> {
    const createResponse = this.page.waitForResponse(
      (res) =>
        res.request().method() === 'POST' &&
        /\/api\/workspaces\/\d+\/pages$/.test(res.url()) &&
        res.ok(),
      { timeout: 10000 }
    );

    await triggerCreate();
    const response = await createResponse;
    const body = (await response.json()) as { id: number };
    const newId = body.id;

    await this.page.waitForURL(new RegExp(`/workspaces/${workspaceId}/pages/${newId}\\b`), {
      timeout: 10000,
    });
    // The placeholder title ("Untitled") is what the server stored;
    // wait until PagesView mounts the title input then replace it.
    await this.titleInput.waitFor({ state: 'visible', timeout: 10000 });
    await this.titleInput.fill(title);
    // Autosave debounces ~1.2s after the last input — wait for the
    // resulting PUT before returning so subsequent assertions see the
    // persisted title.
    await this.waitForAutosave(workspaceId, newId);
    await expect(this.titleInput).toHaveValue(title, { timeout: 10000 });
    return newId;
  }

  /**
   * Wait for the next autosave PUT to land for the given page. Used
   * after a programmatic title/content edit when the assertion needs
   * the server state to catch up.
   */
  async waitForAutosave(workspaceId: string, pageId: number) {
    await this.page.waitForResponse(
      (res) =>
        res.request().method() === 'PUT' &&
        res.url().endsWith(`/api/workspaces/${workspaceId}/pages/${pageId}`) &&
        res.ok(),
      { timeout: 10000 }
    );
    await expect(this.saveStatus).toHaveAttribute('data-status', 'saved', {
      timeout: 5000,
    });
  }

  /**
   * Set page content via the REST API and reload — Milkdown's markdown
   * input rules don't reliably fire on `keyboard.insertText`, so typing
   * `# heading` does not produce a heading node in draftContent. Tests
   * that care about the parsed content (TOC, persisted markdown) seed
   * via the API and reload to assert end-to-end persistence.
   */
  async setContentViaAPI(workspaceId: string, pageId: number, title: string, content: string) {
    const response = await this.page.request.put(`/api/workspaces/${workspaceId}/pages/${pageId}`, {
      data: { title, content },
    });
    if (!response.ok()) {
      throw new Error(`setContentViaAPI failed: ${response.status()} ${await response.text()}`);
    }
    await this.page.reload();
    await this.page.waitForLoadState('networkidle');
    await expect(this.titleInput).toHaveValue(title, { timeout: 10000 });
  }

  getCurrentPageId(): number {
    const match = this.page.url().match(/\/pages\/(\d+)/);
    if (!match) throw new Error(`No page id in URL: ${this.page.url()}`);
    return Number(match[1]);
  }

  treeItem(pageId: number | string): Locator {
    return this.page.getByTestId(`page-tree-item-${pageId}`);
  }

  /**
   * Click the page's title button in the sidebar tree. The kebab `...`
   * trigger sits next to the title and would intercept generic
   * .locator('button') clicks, so we target `.page-button` directly.
   */
  async selectPage(pageId: number | string) {
    await this.treeItem(pageId).locator('.page-button').click();
    await this.page.waitForURL(new RegExp(`/pages/${pageId}\\b`), {
      timeout: 5000,
    });
    await this.titleInput.waitFor({ state: 'visible', timeout: 5000 });
  }

  async setTitle(title: string) {
    await this.titleInput.click();
    await this.titleInput.fill(title);
  }

  /**
   * Replace editor body with Markdown. Selecting-all + insertText is the
   * pattern used elsewhere in the suite for the Milkdown editor.
   */
  async setContent(markdown: string) {
    await this.editor.waitFor({ state: 'visible', timeout: 10000 });
    await this.editor.click();
    await this.page.keyboard.press(process.platform === 'darwin' ? 'Meta+A' : 'Control+A');
    await this.page.keyboard.press('Delete');
    await this.page.keyboard.insertText(markdown);
  }

  /** Back-compat shim for the old explicit-save API; autosave makes
   *  the click a no-op so callers can keep using save() as a barrier. */
  async save(workspaceId: string, pageId: number) {
    await this.waitForAutosave(workspaceId, pageId);
  }

  /**
   * Open the right-pane `...` menu and click the named item. The kebab
   * uses Melt UI's popover; menu items are rendered with the items[]
   * config and `title` shows as the visible label.
   */
  private async openToolbarMenuItem(label: string) {
    await this.toolbarKebab.click();
    const item = this.page.locator(`[data-menu-item]`, { hasText: label }).first();
    await item.waitFor({ state: 'visible', timeout: 5000 });
    await item.click();
  }

  async archiveSelected(workspaceId: string, pageId: number) {
    const archiveResponse = this.page.waitForResponse(
      (res) =>
        res.request().method() === 'DELETE' &&
        res.url().endsWith(`/api/workspaces/${workspaceId}/pages/${pageId}`),
      { timeout: 10000 }
    );
    await this.openToolbarMenuItem('Archive');
    await this.page
      .locator('[data-testid="dialog-confirm"]')
      .waitFor({ state: 'visible', timeout: 5000 });
    await this.page.locator('[data-testid="dialog-confirm"]').click();
    await archiveResponse;
    await this.page.waitForURL(new RegExp(`/workspaces/${workspaceId}/pages$`), { timeout: 10000 });
  }

  async openMoveDialog() {
    // PageMoveDialog fires a /pages/tree GET via its loadCandidates effect
    // the moment isOpen flips true. Wait for that response so the picker's
    // options array is populated before the test interacts with it.
    const candidatesLoaded = this.page.waitForResponse(
      (res) =>
        res.request().method() === 'GET' &&
        /\/api\/workspaces\/\d+\/pages\/tree$/.test(res.url()) &&
        res.ok(),
      { timeout: 10000 }
    );
    await this.openToolbarMenuItem('Move');
    await this.page.locator('#page-move-picker').waitFor({ state: 'visible', timeout: 5000 });
    await candidatesLoaded;
  }

  /**
   * Confirm a move after the user has chosen a destination in the picker.
   */
  async confirmMove(workspaceId: string, pageId: number) {
    const moveResponse = this.page.waitForResponse(
      (res) =>
        res.request().method() === 'POST' &&
        res.url().includes(`/api/workspaces/${workspaceId}/pages/${pageId}/move`) &&
        res.ok(),
      { timeout: 10000 }
    );
    await this.page.locator('[data-testid="page-move-confirm"]').click();
    await moveResponse;
  }

  /**
   * Confirm a move by pressing Enter instead of clicking the button
   * (WI-203). After a destination is picked, focus sits in the picker
   * input with its dropdown closed, so Enter bubbles up to the Modal's
   * onSubmit hook rather than being consumed by the picker.
   */
  async confirmMoveWithEnter(workspaceId: string, pageId: number) {
    const moveResponse = this.page.waitForResponse(
      (res) =>
        res.request().method() === 'POST' &&
        res.url().includes(`/api/workspaces/${workspaceId}/pages/${pageId}/move`) &&
        res.ok(),
      { timeout: 10000 }
    );
    await this.page.locator('#page-move-picker').press('Enter');
    await moveResponse;
  }

  /**
   * Pick a move destination. BasePicker renders each option with
   * `data-option-value="<id>"`. The single-select input uses Melt UI's
   * combobox builder — `.click()` alone doesn't reliably open the
   * dropdown under Playwright, so type the title to filter and then
   * click the matching option.
   */
  async pickMoveCandidate(candidateId: number, candidateTitle: string) {
    const picker = this.page.locator('#page-move-picker');
    await picker.click();
    await picker.fill(candidateTitle);
    const option = this.page.locator(`[data-option-value="${candidateId}"]`).first();
    await option.waitFor({ state: 'visible', timeout: 10000 });
    await option.click();
  }
}
