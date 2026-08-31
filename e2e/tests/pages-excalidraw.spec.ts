import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { KnowledgePage } from '../pages/knowledge.page';
import { WorkspacePage } from '../pages/workspace.page';

/**
 * Excalidraw-in-pages Milkdown extension. The full Excalidraw editing
 * surface lives behind a React canvas we don't want to drive through
 * Playwright, so this spec verifies the editor integration without
 * drawing:
 *
 *   1. The Diagram toolbar button shows up on a knowledge page.
 *   2. Clicking it opens PageDiagramModal.
 *   3. A page whose markdown contains an `excalidraw` fenced block
 *      renders the diagram node-view when reopened, proving the
 *      parseMarkdown round-trip works end-to-end.
 */
test.describe('Knowledge Pages — Excalidraw diagram blocks', () => {
  test.describe.configure({ retries: 0 });

  let workspaceId: string;
  let knowledge: KnowledgePage;

  test.beforeAll(async ({ browser }, workerInfo) => {
    const context = await browser.newContext();
    const page = await context.newPage();
    const workspacePage = new WorkspacePage(page);
    const data = generateWorkspace(`pages-excalidraw-${workerInfo.workerIndex}`);
    await workspacePage.createWorkspace(data);
    workspaceId = await workspacePage.getWorkspaceId(data.name);
    await context.close();
  });

  test.beforeEach(async ({ page }) => {
    knowledge = new KnowledgePage(page);
  });

  test('toolbar exposes a Diagram button that opens the modal', async ({ page }) => {
    await knowledge.createRootPage(workspaceId, 'Architecture');

    const button = page.getByTestId('milkdown-insert-diagram');
    await expect(button).toBeVisible();
    await button.click();

    const modal = page.getByTestId('page-diagram-modal');
    await expect(modal).toBeVisible();

    // Cancel out — no unsaved changes yet, so the discard confirm shouldn't
    // appear and the modal should just close.
    await modal.getByTestId('page-diagram-cancel').click();
    await expect(modal).toBeHidden();
  });

  test('deletes a diagram block and persists its removal', async ({ page }) => {
    const pageId = await knowledge.createRootPage(workspaceId, 'Disposable Architecture');

    await page.getByTestId('milkdown-insert-diagram').click();
    await page.getByTestId('page-diagram-name').fill('Temporary flow');
    const createResponse = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        response.url().endsWith(`/api/workspaces/${workspaceId}/pages/${pageId}/diagrams`) &&
        response.status() === 201
    );
    await page.getByTestId('page-diagram-save').click();
    await createResponse;
    await expect(page.getByTestId('page-diagram-modal')).toHaveCount(0);

    const block = page.getByTestId('page-diagram-block');
    await expect(block).toBeVisible({ timeout: 10_000 });
    await block.getByTestId('excalidraw-block-delete').click();
    await expect(page.getByTestId('dialog-confirm')).toBeVisible();
    await page.getByTestId('dialog-cancel').click();
    await expect(block).toBeVisible();

    await block.getByTestId('excalidraw-block-delete').click();
    await expect(page.getByTestId('dialog-confirm')).toBeVisible();

    const saveResponse = page.waitForResponse(
      (response) =>
        response.request().method() === 'PUT' &&
        response.url().endsWith(`/api/workspaces/${workspaceId}/pages/${pageId}`) &&
        response.ok()
    );
    await page.getByTestId('dialog-confirm').click();
    await saveResponse;

    await expect(block).toHaveCount(0);
    await page.reload();
    await expect(page.getByTestId('page-diagram-block')).toHaveCount(0);
  });

  test('human create, reload, edit, and revision restore keep immutable diagrams renderable', async ({
    page,
  }) => {
    const pageId = await knowledge.createRootPage(workspaceId, 'Editable Architecture');

    await page.getByTestId('milkdown-insert-diagram').click();
    await expect(page.getByTestId('page-diagram-modal')).toBeVisible();
    await page.getByTestId('page-diagram-name').fill('Initial flow');

    const createResponse = page.waitForResponse(
      (res) =>
        res.request().method() === 'POST' &&
        res.url().endsWith(`/api/workspaces/${workspaceId}/pages/${pageId}/diagrams`) &&
        res.status() === 201
    );
    await page.getByTestId('page-diagram-save').click();
    const created = (await (await createResponse).json()) as {
      attachment_id: number;
      revision_number: number;
    };

    const block = page.getByTestId('page-diagram-block');
    await expect(block).toBeVisible({ timeout: 10_000 });
    await expect(block.getByTestId('page-diagram-caption')).toHaveText('Initial flow');

    await page.reload();
    await page.waitForLoadState('networkidle');
    await expect(page.getByTestId('page-diagram-block')).toBeVisible({
      timeout: 10_000,
    });
    await expect(page.getByTestId('page-diagram-caption')).toHaveText('Initial flow');

    await page.getByTestId('excalidraw-block-edit').click();
    await expect(page.getByTestId('page-diagram-modal')).toBeVisible();
    await page.getByTestId('page-diagram-name').fill('Edited flow');

    const updateResponse = page.waitForResponse(
      (res) =>
        res.request().method() === 'PUT' &&
        res
          .url()
          .endsWith(
            `/api/workspaces/${workspaceId}/pages/${pageId}/diagrams/${created.attachment_id}`
          ) &&
        res.ok()
    );
    await page.getByTestId('page-diagram-save').click();
    const updated = (await (await updateResponse).json()) as {
      attachment_id: number;
    };
    expect(updated.attachment_id).not.toBe(created.attachment_id);
    await expect(page.getByTestId('page-diagram-caption')).toHaveText('Edited flow', {
      timeout: 10_000,
    });

    await page.reload();
    await page.waitForLoadState('networkidle');
    await expect(page.getByTestId('page-diagram-caption')).toHaveText('Edited flow', {
      timeout: 10_000,
    });

    await page.getByTestId('page-toolbar-kebab').click();
    await page.getByTestId('page-menu-history').click();
    const drawer = page.getByTestId('pages-history-drawer');
    await expect(drawer).toBeVisible();
    // History is newest-first: after the replacement, the create revision is
    // the second row. Pin its server-returned revision number before restoring.
    const revisionRow = drawer.getByTestId('pages-history-row').nth(1);
    await expect(revisionRow).toHaveAttribute('data-revision', String(created.revision_number));
    await revisionRow.click();

    const restoreResponse = page.waitForResponse(
      (res) =>
        res.request().method() === 'POST' &&
        res.url().includes(`/api/workspaces/${workspaceId}/pages/${pageId}/history/`) &&
        res.url().endsWith('/restore') &&
        res.ok()
    );
    await revisionRow.getByTestId('pages-history-restore').click();
    await page.getByTestId('dialog-confirm').click();
    await restoreResponse;

    await expect(page.getByTestId('page-diagram-caption')).toHaveText('Initial flow', {
      timeout: 10_000,
    });
    await expect(page.getByTestId('page-diagram-canvas')).toBeVisible();
  });

  test('a page with an excalidraw fence renders the diagram node-view', async ({ page }) => {
    const pageId = await knowledge.createRootPage(workspaceId, 'System Diagram');

    // Seed a minimal Excalidraw scene as a page attachment so the node view
    // has a real ID to fetch. Empty elements is enough: exportToSvg returns
    // a valid SVG and the test asserts the rendered block contract.
    const scene = Buffer.from(JSON.stringify({ elements: [], appState: {}, files: {} }));
    const uploadRes = await page.request.post('/api/attachments/upload', {
      multipart: {
        entity_type: 'page',
        entity_id: String(pageId),
        file: {
          name: 'diagram.json',
          mimeType: 'application/json',
          buffer: scene,
        },
      },
    });
    expect(uploadRes.ok(), `upload failed: ${await uploadRes.text()}`).toBeTruthy();
    const { attachment } = (await uploadRes.json()) as {
      attachment: { id: number };
    };

    const fenced =
      '```excalidraw\n' +
      JSON.stringify({ attachmentId: attachment.id, name: 'Topology' }) +
      '\n```\n';
    await knowledge.setContentViaAPI(workspaceId, pageId, 'System Diagram', fenced);

    // Diagram block + caption + ready canvas render on reload, proving both
    // the parseMarkdown round-trip and successful attachment-to-SVG loading.
    const block = page.getByTestId('page-diagram-block').first();
    await expect(block).toBeVisible({ timeout: 10000 });
    await expect(block.getByTestId('page-diagram-caption')).toHaveText('Topology');
    await expect(block).toHaveAttribute('data-status', 'ready', {
      timeout: 10000,
    });
  });

  test('a mermaid fence renders inline as an SVG', async ({ page }) => {
    const pageId = await knowledge.createRootPage(workspaceId, 'Mermaid Page');
    // Trivial graph — keeps the spec resilient to mermaid renderer changes,
    // we only care that *some* svg appears inside the mermaid block view.
    const fenced = '```mermaid\ngraph LR\n  A-->B\n  B-->C\n```\n';
    await knowledge.setContentViaAPI(workspaceId, pageId, 'Mermaid Page', fenced);

    const block = page.getByTestId('page-mermaid-block').first();
    await expect(block).toBeVisible({ timeout: 10000 });
    await expect(block.getByTestId('page-mermaid-canvas')).toHaveAttribute('data-status', 'ready', {
      timeout: 10000,
    });
  });
});
