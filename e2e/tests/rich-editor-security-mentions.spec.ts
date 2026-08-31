import {
  authenticateAdminRequest,
  createItemViaAPI,
  createWorkspaceViaAPI,
} from '../fixtures/api-helpers';
import { type APIRequestContext, expect, test } from '../fixtures/context-path';
import { generateItem, generateUser, generateWorkspace } from '../fixtures/test-data';

/**
 * Browser contracts for safe Markdown display and rich-editor mentions.
 * HTTP source/render invariants live in tests/e2e_security_contracts_test.go.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

interface Notification {
  id: number;
  type: string;
  action_url?: string;
}

async function loginCtx(
  request: APIRequestContext,
  username: string,
  password: string
): Promise<void> {
  const resp = await request.post('/api/auth/login', {
    headers: SEC_FETCH,
    data: { email_or_username: username, password, remember_me: false },
  });
  expect(resp.ok(), `login as ${username}: ${resp.status()}`).toBeTruthy();
}

async function waitForMention(
  request: APIRequestContext,
  itemId: number,
  timeoutMs = 15_000
): Promise<Notification> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const resp = await request.get('/api/notifications', {
      headers: SEC_FETCH,
    });
    if (resp.ok()) {
      const body = await resp.json();
      const list = (body?.data ?? body?.notifications ?? body ?? []) as Notification[];
      const match = list.find(
        (n) => n.type === 'mention' && n.action_url?.includes(`/items/${itemId}`)
      );
      if (match) return match;
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  throw new Error(`no mention notification for item ${itemId} within ${timeoutMs}ms`);
}

test.describe('Rich editor security + mentions', () => {
  test('untrusted Markdown remains visible without creating executable DOM', async ({
    page,
    request,
  }, testInfo) => {
    await authenticateAdminRequest(request);

    const suffix = `xss${Date.now()}w${testInfo.workerIndex}`;
    const ws = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
    const description =
      'Visible <script>window.__markdownXSS = true</script> <img src="x" onerror="window.__markdownXSS = true"> [bad](javascript:alert(1))';
    const item = await createItemViaAPI(request, ws.id, {
      title: `Promise<Anything> ${suffix}`,
      description,
    });

    await page.goto(`/workspaces/${ws.id}/items/${item.id}`);
    const display = page.getByTestId('item-description-display');
    await expect(display).toBeVisible({ timeout: 15_000 });
    await expect(display).toContainText('<script>');
    await expect(display.locator('script')).toHaveCount(0);
    await expect(display.locator('img')).toHaveCount(0);
    expect(await page.evaluate(() => Reflect.has(window, '__markdownXSS'))).toBe(false);
  });

  test(
    'typing @member in the comment composer triggers MentionPicker, selection notifies the member',
    {
      tag: '@critical-browser',
    },
    async ({ page, request, browser }, testInfo) => {
      test.setTimeout(60_000);
      await authenticateAdminRequest(request);

      const suffix = `rem${Date.now()}w${testInfo.workerIndex}r${testInfo.repeatEachIndex}`;
      const ws = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
      const itemData = generateItem(ws.id, suffix);
      const item = await createItemViaAPI(request, ws.id, {
        title: itemData.title,
        description: itemData.description,
      });

      // Create the member who will be mentioned, with Editor on the same
      // workspace so the mention-service visibility gate (mention_service.go:225)
      // resolves the notification — without item.view permission the mention
      // would be silently swallowed.
      const userData = generateUser(suffix);
      const createResp = await request.post(`${BASE_URL}/api/users`, {
        headers: SEC_FETCH,
        data: {
          email: userData.email,
          username: userData.username,
          first_name: userData.first_name,
          last_name: userData.last_name,
          password: userData.password_hash,
        },
      });
      expect(
        createResp.ok(),
        `user create: ${createResp.status()} ${await createResp.text()}`
      ).toBeTruthy();
      const member = await createResp.json();
      const activateResp = await request.post(`${BASE_URL}/api/users/${member.id}/activate`, {
        headers: SEC_FETCH,
      });
      expect(activateResp.ok(), `activate member: ${activateResp.status()}`).toBeTruthy();

      const rolesResp = await request.get('/api/workspace-roles', {
        headers: SEC_FETCH,
      });
      const rolesBody = await rolesResp.json();
      const roles: Array<{ id: number; name: string }> = rolesBody.data ?? rolesBody;
      const editorRole = roles.find((r) => r.name === 'Editor');
      if (!editorRole) throw new Error('Editor role missing from seed');
      const assignmentResp = await request.post('/api/workspace-roles/assign', {
        headers: SEC_FETCH,
        data: {
          user_id: member.id,
          workspace_id: ws.id,
          role_id: editorRole.id,
        },
      });
      expect(
        assignmentResp.ok(),
        `assign Editor role: ${assignmentResp.status()} ${await assignmentResp.text()}`
      ).toBeTruthy();

      // Build a member-authenticated API context for the post-mention
      // notification assertion. Empty storageState so we don't inherit admin's
      // session.
      const memberCtx = await browser.newContext({
        storageState: { cookies: [], origins: [] },
        baseURL: BASE_URL,
      });
      try {
        await loginCtx(memberCtx.request, userData.username, userData.password_hash);

        // Role assignment invalidates shared permission caches. Confirm this
        // context observes the grant before exercising the mention visibility
        // gate, especially when the full suite has several workers mutating roles.
        await expect
          .poll(
            async () =>
              (
                await memberCtx.request.get(`/api/items/${item.id}`, {
                  headers: SEC_FETCH,
                })
              ).status(),
            { timeout: 10_000 }
          )
          .toBe(200);

        // Admin opens the item detail in their browser tab.
        await page.goto(`/workspaces/${ws.id}/items/${item.id}`);
        await expect(page.locator('[data-testid="comments-section"]')).toBeVisible({
          timeout: 15_000,
        });

        // The composer test id appears on the editable node only after Milkdown
        // is ready, so typing cannot race its asynchronous initialization.
        const composer = page.getByTestId('comment-composer');
        await expect(composer).toBeVisible({ timeout: 15_000 });
        await expect(composer).toHaveAttribute('data-ready', 'true', {
          timeout: 15_000,
        });
        await composer.click();

        // Typing `@` + a filter must surface the matching member. Select it via
        await page.keyboard.type('@');
        const picker = page.getByTestId('mention-picker');
        await expect(picker).toBeVisible({ timeout: 5000 });
        await page.keyboard.type(suffix.slice(-6));
        const memberOption = picker.getByTestId('mention-option');
        await expect(memberOption).toHaveCount(1, { timeout: 5000 });
        await expect(memberOption).toBeVisible({ timeout: 5000 });
        await memberOption.click();
        await expect(picker).toBeHidden({ timeout: 3000 });

        // The picker replaces the in-progress query with the selected user's
        // display-name mention. Verify that transaction landed before adding
        // the remainder of the comment and submitting it.
        await expect(composer).toContainText(userData.first_name, {
          timeout: 5000,
        });
        await page.keyboard.insertText('please review');

        // Blur the editor — the MilkdownEditor toolbar can swap visibility
        // on focus changes, shifting the submit button just enough for the
        // click-stability retry loop to spin. Tab moves focus out cleanly.
        await page.keyboard.press('Tab');

        const submitPromise = page.waitForResponse(
          (resp) =>
            resp.url().includes(`/api/items/${item.id}/comments`) &&
            resp.request().method() === 'POST',
          { timeout: 15_000 }
        );
        const submitBtn = page.getByTestId('comment-submit');
        await expect(submitBtn).toBeEnabled({ timeout: 5000 });
        await submitBtn.click();
        const submitResp = await submitPromise;
        expect(submitResp.ok(), `comment submit: ${submitResp.status()}`).toBeTruthy();
        const submittedComment = await submitResp.json();
        const submittedContent = submittedComment.content ?? submittedComment.data?.content ?? '';
        expect(submittedContent).toContain(userData.first_name);

        // The member's notifications must include a mention notification for
        // this item — verifies the @username made it through the editor's
        // serialization and the backend mention_service extracted it.
        const mention = await waitForMention(memberCtx.request, item.id);
        expect(mention.type).toBe('mention');
      } finally {
        await memberCtx.close();
      }
    }
  );
});
