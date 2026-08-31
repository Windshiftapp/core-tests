import { authenticateAdminRequest, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { KnowledgePage } from '../pages/knowledge.page';

test('page edits persist and render in the print view', {
  tag: '@critical-browser',
}, async ({ page, request }) => {
  await authenticateAdminRequest(request);
  const suffix = `critical-page-${Date.now()}`;
  const workspace = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
  const knowledge = new KnowledgePage(page);

  // Keep the headless browser out of the native print dialog while still
  // exercising the application's print route and print-media rendering.
  await page.context().addInitScript(() => {
    window.print = () => {};
  });

  const pageId = await knowledge.createRootPage(String(workspace.id), `Critical page ${suffix}`);
  const marker = `edited-body-${suffix}`;
  await knowledge.editor.click();
  await knowledge.editor.pressSequentially(marker, { delay: 10 });
  await knowledge.save(String(workspace.id), pageId);

  await page.reload();
  await expect(knowledge.editor).toContainText(marker, { timeout: 10_000 });

  await page.goto(`/workspaces/${workspace.id}/pages/${pageId}/print`);
  const printBody = page.locator('[data-testid="page-print-body"] .ProseMirror');
  await expect(printBody).toContainText(marker, { timeout: 15_000 });
  await expect(page.getByTestId('page-print-button')).toBeVisible();

  await page.emulateMedia({ media: 'print' });
  await expect(page.getByTestId('page-print-button')).toBeHidden();
});
