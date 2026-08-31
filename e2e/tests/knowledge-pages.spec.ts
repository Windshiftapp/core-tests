import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { KnowledgePage } from '../pages/knowledge.page';
import { WorkspacePage } from '../pages/workspace.page';

/**
 * Knowledge Pages happy-path E2E coverage.
 *
 * Locks down the user journey through the workspace knowledge / wiki
 * feature: empty state, create root, create child, edit + save, deep
 * link, TOC, archive, and the move dialog. Permission-sensitive flows
 * live in `knowledge-pages-permissions.spec.ts`.
 */
test.describe('Knowledge Pages — happy path', () => {
  // Tests share one workspace via beforeAll, so concurrent creates by
  // different tests would race in the tree. Run sequentially within this
  // describe so each test observes a known set of pages.
  test.describe.configure({ mode: 'serial', retries: 0 });

  let workspaceId: string;
  let knowledge: KnowledgePage;

  // One workspace per file keeps the tree fixtures isolated from other specs
  // while letting individual tests share setup. test.describe.configure
  // would also work, but `beforeAll` matches the pattern used elsewhere.
  test.beforeAll(async ({ browser }) => {
    const context = await browser.newContext();
    const page = await context.newPage();
    const workspacePage = new WorkspacePage(page);
    const data = generateWorkspace('knowledge-happy');
    await workspacePage.createWorkspace(data);
    workspaceId = await workspacePage.getWorkspaceId(data.name);
    await context.close();
  });

  test.beforeEach(async ({ page }) => {
    knowledge = new KnowledgePage(page);
  });

  test('empty tree shows the empty-state CTA', async ({ page }) => {
    await knowledge.gotoIndex(workspaceId);

    // The EmptyState card renders with the i18n title "No pages yet" (en).
    // Avoid matching the heading by exact copy — the tree-empty container
    // is the stable scope.
    await expect(page.locator('.tree-empty')).toBeVisible();
    await expect(knowledge.tree).toHaveCount(0);
  });

  test('creates a root page and reflects it in the URL', async () => {
    await knowledge.gotoIndex(workspaceId);
    const pageId = await knowledge.createRootPage(workspaceId, 'Onboarding');

    await expect(knowledge.page).toHaveURL(
      new RegExp(`/workspaces/${workspaceId}/pages/${pageId}\\b`)
    );
    await expect(knowledge.treeItem(pageId)).toBeVisible();
    await expect(knowledge.titleInput).toHaveValue('Onboarding');
  });

  test('creates a child page under the selected parent', async () => {
    await knowledge.gotoIndex(workspaceId);

    // Need a clean root for this scenario — the spec shares state across
    // tests so we create a fresh parent rather than reusing the prior page.
    const parentId = await knowledge.createRootPage(workspaceId, 'Handbook');
    // Selection is already the parent post-create (PagesView sets selectedId
    // to the newly created page via navigate); proceed straight to child.
    const childId = await knowledge.createChildPage(workspaceId, 'Conventions');

    const child = knowledge.treeItem(childId);
    await expect(child).toBeVisible();
    // Children are rendered depth-first; the indent is driven by `depth`
    // (style padding-left: 1 + depth*0.75rem). Depth=1 -> 1.75rem.
    await expect(child).toHaveAttribute('style', /padding-left:\s*1\.75rem/);
    // Parent should still be on the tree.
    await expect(knowledge.treeItem(parentId)).toBeVisible();
  });

  test('persists title and body edits across reload', async ({ page }) => {
    await knowledge.gotoIndex(workspaceId);
    const pageId = await knowledge.createRootPage(workspaceId, 'Pre-edit');

    // Edit the title through the UI to exercise the save flow with a dirty
    // state. Body editing through keyboard.insertText doesn't reliably mark
    // draftContent dirty in Milkdown, so we type the title and let save
    // round-trip persistence be the assertion. The body-persistence path is
    // covered by the TOC test, which seeds content via the API.
    await knowledge.setTitle('Edited title');
    await knowledge.save(workspaceId, pageId);

    await page.reload();
    await page.waitForLoadState('networkidle');

    await expect(knowledge.titleInput).toHaveValue('Edited title');
  });

  test('body edit typed into the Milkdown editor persists across reload', async ({ page }) => {
    await knowledge.gotoIndex(workspaceId);
    const pageId = await knowledge.createRootPage(workspaceId, 'Body editor');

    // pressSequentially with a small delay fires per-key input events into
    // ProseMirror, which marks draftContent dirty even without input-rule
    // triggers (`# heading`, `**bold**`). Plain ASCII text is enough — the
    // assertion is "what I typed comes back after a reload", not Markdown
    // parsing. Markdown round-trips are covered by the TOC test.
    const marker = `body-typed-${Date.now()}`;
    await knowledge.editor.waitFor({ state: 'visible', timeout: 10_000 });
    await knowledge.editor.click();
    await knowledge.editor.pressSequentially(marker, { delay: 10 });
    await knowledge.save(workspaceId, pageId);

    await page.reload();
    await page.waitForLoadState('networkidle');

    await expect(knowledge.editor).toContainText(marker, { timeout: 10_000 });
  });

  test('deep-links straight to a page via URL', async ({ page }) => {
    await knowledge.gotoIndex(workspaceId);
    const pageId = await knowledge.createRootPage(workspaceId, 'Deep link');

    // Throw away the in-app navigation and load the URL cold.
    await page.goto('about:blank');
    await knowledge.gotoPage(workspaceId, pageId);

    await expect(knowledge.titleInput).toHaveValue('Deep link');
    await expect(knowledge.treeItem(pageId)).toBeVisible();
  });

  test('renders a TOC entry per heading in the body', async () => {
    await knowledge.gotoIndex(workspaceId);
    const pageId = await knowledge.createRootPage(workspaceId, 'TOC page');

    // Seed content via the REST API — typing into Milkdown via keyboard
    // doesn't fire the markdown input rules under Playwright control, so
    // headings don't end up in the stored markdown. The TOC pipeline we
    // want to test is markdownToc.parseMarkdownHeadings → headings array →
    // toc render, which only needs the stored content to contain headings.
    await knowledge.setContentViaAPI(
      workspaceId,
      pageId,
      'TOC page',
      '# Top heading\n\nintro\n\n## Section one\n\none\n\n## Section two\n\ntwo'
    );

    // The TOC is only rendered in Read mode — Edit mode hides it so
    // the writer keeps the full pane width.
    await knowledge.switchToReadMode();

    await expect(knowledge.toc).toBeVisible();
    await expect(knowledge.tocEntries).toHaveCount(3);
    await expect(knowledge.tocEntries.nth(0)).toContainText('Top heading');
    await expect(knowledge.tocEntries.nth(1)).toContainText('Section one');
    await expect(knowledge.tocEntries.nth(2)).toContainText('Section two');
  });

  test('archives a leaf page and removes it from the tree', async () => {
    await knowledge.gotoIndex(workspaceId);
    const pageId = await knowledge.createRootPage(workspaceId, 'To archive');

    await knowledge.archiveSelected(workspaceId, pageId);

    // After archive: app routes back to the index and the tree no longer
    // shows the page. Other pages from earlier scenarios are still here.
    await expect(knowledge.treeItem(pageId)).toHaveCount(0);
  });

  test('restores an archived page and returns it to the tree', async ({ page }) => {
    await knowledge.gotoIndex(workspaceId);
    const pageId = await knowledge.createRootPage(workspaceId, 'To restore');
    await knowledge.archiveSelected(workspaceId, pageId);

    await page.getByTestId('pages-archived-open').click();
    await expect(page).toHaveURL(new RegExp(`/workspaces/${workspaceId}/pages/archived$`));
    await expect(page.getByTestId('archived-pages-view')).toBeVisible();

    const restoreButton = page
      .getByTestId(`archived-page-row-${pageId}`)
      .getByTestId('archived-page-unarchive');
    await expect(restoreButton).toBeVisible();

    const restoreResponse = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        response.url().endsWith(`/api/workspaces/${workspaceId}/pages/${pageId}/unarchive`) &&
        response.ok()
    );
    await restoreButton.click();
    await page.getByTestId('dialog-confirm').click();
    await restoreResponse;
    await expect(restoreButton).toHaveCount(0);

    await page.getByTestId('archived-pages-back').click();
    await expect(page).toHaveURL(new RegExp(`/workspaces/${workspaceId}/pages$`));
    await expect(knowledge.treeItem(pageId)).toBeVisible();
  });

  test('move dialog reparents a page to another root', async ({ page }) => {
    await knowledge.gotoIndex(workspaceId);
    const destinationId = await knowledge.createRootPage(workspaceId, 'Destination');
    const moverId = await knowledge.createRootPage(workspaceId, 'Mover');

    // Re-select the mover (createRootPage navigated us to it but we want to
    // make the action explicit) and open the move dialog.
    await knowledge.selectPage(moverId);
    await knowledge.openMoveDialog();
    await knowledge.pickMoveCandidate(destinationId, 'Destination');
    await knowledge.confirmMove(workspaceId, moverId);

    // Reload to force a fresh tree fetch (the dialog's onMoved triggers
    // loadTree, but the deeper assertion of depth-first ordering is most
    // reliable post-reload).
    await page.reload();
    await page.waitForLoadState('networkidle');

    const mover = knowledge.treeItem(moverId);
    await expect(mover).toBeVisible();
    // Mover should now be indented one level under Destination.
    await expect(mover).toHaveAttribute('style', /padding-left:\s*1\.75rem/);
    await expect(knowledge.treeItem(destinationId)).toBeVisible();
  });

  test('move dialog confirms with the Enter hotkey', async ({ page }) => {
    // WI-203: the dialog used to be mouse-only. The Modal's onSubmit hook
    // now wires Enter to confirmMove. The picker still owns Enter while
    // its dropdown is open (it selects the highlighted option), so the
    // flow is: pick a destination, then Enter fires the move.
    await knowledge.gotoIndex(workspaceId);
    const destinationId = await knowledge.createRootPage(workspaceId, 'Hotkey dest');
    const moverId = await knowledge.createRootPage(workspaceId, 'Hotkey mover');

    await knowledge.selectPage(moverId);
    await knowledge.openMoveDialog();
    await knowledge.pickMoveCandidate(destinationId, 'Hotkey dest');
    await knowledge.confirmMoveWithEnter(workspaceId, moverId);

    // Success path closes the dialog.
    await expect(page.locator('[data-testid="page-move-confirm"]')).toHaveCount(0);

    await page.reload();
    await page.waitForLoadState('networkidle');

    const mover = knowledge.treeItem(moverId);
    await expect(mover).toBeVisible();
    await expect(mover).toHaveAttribute('style', /padding-left:\s*1\.75rem/);
  });
});
