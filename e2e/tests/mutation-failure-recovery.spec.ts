import { createItemViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';

test.describe('Browser mutation, bootstrap, and offline recovery (WI-699)', () => {
  test('failed item save retains input and writes exactly once on retry', async ({
    page,
    request,
  }) => {
    const stamp = Date.now();
    const workspace = await createWorkspaceViaAPI(
      request,
      generateWorkspace(`wi699-item-save-${stamp}`)
    );
    const originalTitle = `Original title ${stamp}`;
    const recoveredTitle = `Recovered title ${stamp}`;
    const item = await createItemViaAPI(request, workspace.id, {
      title: originalTitle,
    });

    let attempts = 0;
    let forwardedWrites = 0;
    await page.route(`**/api/items/${item.id}`, async (route, interceptedRequest) => {
      if (interceptedRequest.method() !== 'PUT') {
        await route.continue();
        return;
      }

      attempts += 1;
      if (attempts === 1) {
        await route.fulfill({
          status: 503,
          contentType: 'application/json',
          body: JSON.stringify({ error: 'Temporary mutation failure' }),
        });
        return;
      }

      forwardedWrites += 1;
      await route.continue();
    });

    await page.goto(`/workspaces/${workspace.id}/items/${item.id}`);
    const titleButton = page.getByTestId('item-title-edit');
    await expect(titleButton).toHaveText(originalTitle, { timeout: 15_000 });
    await titleButton.click();

    const titleInput = page.getByTestId('item-title-input');
    await expect(titleInput).toHaveValue(originalTitle);
    await expect(titleInput).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)');
    await titleInput.fill(recoveredTitle);

    const firstAttempt = page.waitForRequest(
      (outgoing) =>
        outgoing.method() === 'PUT' &&
        new URL(outgoing.url()).pathname.endsWith(`/api/items/${item.id}`)
    );
    await titleInput.press('Enter');
    await firstAttempt;

    await expect(page.getByTestId('toast').first()).toHaveAttribute('data-toast-variant', 'error');
    await expect(titleInput).toBeVisible();
    await expect(titleInput).toHaveValue(recoveredTitle);
    expect(attempts).toBe(1);
    expect(forwardedWrites).toBe(0);

    const successfulRetry = page.waitForResponse(
      (response) =>
        response.request().method() === 'PUT' &&
        new URL(response.url()).pathname.endsWith(`/api/items/${item.id}`)
    );
    await titleInput.press('Enter');
    expect((await successfulRetry).ok()).toBeTruthy();

    await expect(titleInput).toBeHidden();
    await expect(titleButton).toHaveText(recoveredTitle);
    expect(attempts).toBe(2);
    expect(forwardedWrites).toBe(1);

    await page.reload();
    await expect(page.getByTestId('item-title-edit')).toHaveText(recoveredTitle);
    expect(attempts).toBe(2);
    expect(forwardedWrites).toBe(1);
  });

  test('aborted comment keeps its draft and creates no phantom before retry', async ({
    page,
    request,
  }) => {
    const stamp = Date.now();
    const workspace = await createWorkspaceViaAPI(
      request,
      generateWorkspace(`wi699-comment-${stamp}`)
    );
    const item = await createItemViaAPI(request, workspace.id, {
      title: `WI-699 comment ${stamp}`,
    });
    const commentText = `Recoverable comment ${stamp}`;

    let attempts = 0;
    let forwardedWrites = 0;
    await page.route(`**/api/items/${item.id}/comments`, async (route, interceptedRequest) => {
      if (interceptedRequest.method() !== 'POST') {
        await route.continue();
        return;
      }

      attempts += 1;
      if (attempts === 1) {
        await route.abort('failed');
        return;
      }

      forwardedWrites += 1;
      await route.continue();
    });

    await page.goto(`/workspaces/${workspace.id}/items/${item.id}`);
    const comments = page.getByTestId('comments-section');
    await expect(comments).toBeVisible({ timeout: 15_000 });
    await expect(comments.getByTestId('comment-item')).toHaveCount(0);

    const composer = comments.getByTestId('comment-editor');
    await expect(composer.getByTestId('comment-composer')).toHaveAttribute('data-ready', 'true');
    await composer.click();
    await page.keyboard.insertText(commentText);
    await expect(composer).toContainText(commentText);
    const submit = comments.getByTestId('comment-submit');
    await expect(submit).toBeEnabled();

    await page.keyboard.press('ControlOrMeta+Enter');

    await expect(comments.getByTestId('comments-error')).toBeVisible();
    await expect(composer).toContainText(commentText);
    await expect(comments.getByTestId('comment-item')).toHaveCount(0);
    expect(attempts).toBe(1);
    expect(forwardedWrites).toBe(0);

    const successfulRetry = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' &&
        new URL(response.url()).pathname.endsWith(`/api/items/${item.id}/comments`)
    );
    await page.keyboard.press('ControlOrMeta+Enter');
    expect((await successfulRetry).ok()).toBeTruthy();

    await expect(comments.getByTestId('comment-item')).toHaveCount(1);
    await expect(comments.getByTestId('comment-item')).toContainText(commentText);
    await expect(composer).toHaveText('');
    expect(attempts).toBe(2);
    expect(forwardedWrites).toBe(1);

    await page.reload();
    const reloadedComments = page.getByTestId('comments-section');
    await expect(reloadedComments.getByTestId('comment-item')).toHaveCount(1);
    await expect(reloadedComments.getByTestId('comment-item')).toContainText(commentText);
    expect(attempts).toBe(2);
    expect(forwardedWrites).toBe(1);
  });

  test('failed authenticated bootstrap recovers without rendering login', async ({ page }) => {
    let authAttempts = 0;
    await page.route('**/api/auth/me', async (route) => {
      authAttempts += 1;
      if (authAttempts === 1) {
        await route.fulfill({
          status: 503,
          contentType: 'application/json',
          body: JSON.stringify({ error: 'Temporary bootstrap failure' }),
        });
        return;
      }
      await route.continue();
    });

    await page.goto('/');
    await expect(page.getByTestId('startup-error')).toBeVisible();
    await expect(page.getByTestId('login-dialog')).toHaveCount(0);
    await expect(page.locator('#global-create-button')).toHaveCount(0);

    await page.getByTestId('startup-retry').click();
    await expect(page.getByTestId('startup-error')).toBeHidden();
    await expect(page.locator('#global-create-button')).toBeVisible({
      timeout: 15_000,
    });
    await expect(page.getByTestId('login-dialog')).toHaveCount(0);
    expect(authAttempts).toBe(2);
  });

  test('mobile offline retry preserves route and unsent create input', async ({
    context,
    page,
    request,
  }) => {
    const stamp = Date.now();
    const workspace = await createWorkspaceViaAPI(
      request,
      generateWorkspace(`wi699-mobile-${stamp}`)
    );
    const title = `Offline mobile item ${stamp}`;
    const description = `Draft survives offline ${stamp}`;
    let successfulCreates = 0;

    await page.setViewportSize({ width: 390, height: 844 });
    page.on('response', (response) => {
      if (
        response.request().method() === 'POST' &&
        new URL(response.url()).pathname.endsWith('/api/items') &&
        response.ok()
      ) {
        successfulCreates += 1;
      }
    });

    await page.goto('/m');
    await expect(page.getByTestId('mobile-shell')).toBeVisible({
      timeout: 15_000,
    });
    await expect(page.getByTestId('mobile-nav')).toBeVisible();
    await page.getByTestId('mobile-create-fab').click();
    await expect(page.getByTestId('mobile-create-dialog')).toBeVisible();

    await page.getByTestId('create-workspace').selectOption(String(workspace.id));
    await expect(page.getByTestId('create-type')).not.toHaveValue('');
    await page.getByTestId('create-title').fill(title);
    await page.getByTestId('create-description').fill(description);

    await context.setOffline(true);
    try {
      await page.getByTestId('create-submit').click();
      await expect(page.getByTestId('create-error')).toBeVisible();
      await expect(page).toHaveURL(/\/m$/);
      await expect(page.getByTestId('mobile-shell')).toBeVisible();
      await expect(page.getByTestId('create-title')).toHaveValue(title);
      await expect(page.getByTestId('create-description')).toHaveValue(description);
      expect(successfulCreates).toBe(0);
    } finally {
      await context.setOffline(false);
    }

    await page.getByTestId('create-submit').click();
    await expect(page).toHaveURL(/\/m\/items\/\d+$/, { timeout: 15_000 });
    await expect(page.getByTestId('detail-title')).toHaveText(title);
    expect(successfulCreates).toBe(1);

    await page.reload();
    await expect(page.getByTestId('detail-title')).toHaveText(title);
    expect(successfulCreates).toBe(1);
  });
});
