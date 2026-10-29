import {
  expect,
  test,
  type APIRequestContext,
  type Browser,
  type BrowserContext,
  type Page,
} from '../fixtures/context-path';
import { createWorkspaceViaAPI, createUserViaAPI } from '../fixtures/api-helpers';
import { generateUser, generateWorkspace } from '../fixtures/test-data';
import { createPortalChannel } from '../helpers/portal-setup';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

interface EmailChannelHandle {
  channelId: number;
  name: string;
  workspaceId: number;
}

async function createUnconnectedEmailChannel(
  admin: APIRequestContext,
  suffix: string
): Promise<EmailChannelHandle> {
  const ws = await createWorkspaceViaAPI(admin, generateWorkspace(`email-portal-${suffix}`));

  const create = await admin.post('/api/channels', {
    headers: SEC_FETCH,
    data: {
      name: `Support Mailbox ${suffix}`,
      type: 'email',
      direction: 'inbound',
      status: 'disabled',
    },
  });
  expect(create.ok(), `create email channel: ${create.status()} ${await create.text()}`).toBeTruthy();
  const channel = await create.json();

  // Connection-only config: routing lives on intakes since WI-1644. The
  // channel stays disabled, so full email validation only runs at enable time.
  const config = await admin.put(`/api/channels/${channel.id}/config`, {
    headers: SEC_FETCH,
    data: {
      config: {
        email_auth_method: 'basic',
        imap_host: `imap-${suffix}.example.com`,
        imap_port: 993,
        imap_encryption: 'ssl',
        imap_username: `support-${suffix}@example.com`,
        imap_password: 'imap-secret',
        email_mailbox: 'INBOX',
      },
    },
  });
  expect(config.ok(), `configure email channel: ${config.status()} ${await config.text()}`).toBeTruthy();

  return { channelId: channel.id, name: channel.name, workspaceId: ws.id };
}

async function openEmailChannelSettings(page: Page, channelId: number): Promise<void> {
  // The modal loads the intake list and the portal-option list on open; wait
  // for both so the form is populated before the test interacts with it.
  const intakesLoaded = page.waitForResponse(
    (resp) =>
      resp.url().includes(`/channels/${channelId}/intakes`) &&
      resp.request().method() === 'GET'
  );
  const portalsLoaded = page.waitForResponse(
    (resp) => resp.url().includes('/channels') && resp.url().includes('type=portal')
  );
  await page.goto('/admin/channels');
  await expect(page.getByTestId(`admin-channel-row-${channelId}`)).toBeVisible();
  await page.getByTestId(`admin-channel-row-${channelId}`).click();
  await intakesLoaded;
  await portalsLoaded;
  await expect(page.getByTestId('channel-intake-list')).toBeVisible();
}

// Routing is explicit since WI-1644: pick the target portal, one of the
// workspaces it serves, and the item type created items use.
async function selectPortalTarget(
  page: Page,
  portal: { channelId: number; workspaceId: number }
): Promise<void> {
  await page.locator('#intake-portal').click();
  await page.locator(`#intake-portal-option-${portal.channelId}`).click();
  await page.locator('#intake-workspace').click();
  await page.locator(`#intake-workspace-option-${portal.workspaceId}`).click();
  await page.locator('#intake-item-type').click();
  // The first option is the "select a type" placeholder; pick the first real one.
  await page.getByTestId('intake-item-type-option').nth(1).click();
}

async function addPortalIntake(
  page: Page,
  portal: { channelId: number; workspaceId: number },
  folder: string
): Promise<number> {
  await page.getByTestId('channel-intake-add').click();
  await page.getByTestId('channel-intake-folder').fill(folder);
  await selectPortalTarget(page, portal);
  const created = page.waitForResponse(
    (resp) =>
      /\/channels\/\d+\/intakes$/.test(new URL(resp.url()).pathname) &&
      resp.request().method() === 'POST'
  );
  await page.getByTestId('channel-intake-save').click();
  const resp = await created;
  expect(resp.ok(), `create intake: ${resp.status()} ${await resp.text()}`).toBeTruthy();
  const intake = await resp.json();
  await expect(page.getByTestId(`channel-intake-${intake.id}`)).toBeVisible();
  return intake.id;
}

async function deleteIntake(page: Page, intakeId: number): Promise<void> {
  const deleted = page.waitForResponse(
    (resp) =>
      resp.url().includes(`/channels/`) &&
      resp.url().endsWith(`/intakes/${intakeId}`) &&
      resp.request().method() === 'DELETE'
  );
  await page.getByTestId(`channel-intake-delete-${intakeId}`).click();
  const resp = await deleted;
  expect(resp.status(), `delete intake: ${resp.status()}`).toBe(204);
  await expect(page.getByTestId(`channel-intake-${intakeId}`)).toHaveCount(0);
}

async function browserForUser(
  browser: Browser,
  user: { username: string; password: string }
): Promise<{ context: BrowserContext; page: Page }> {
  const context = await browser.newContext({
    baseURL: BASE_URL,
    storageState: { cookies: [], origins: [] },
  });
  const login = await context.request.post('/api/auth/login', {
    headers: SEC_FETCH,
    data: {
      email_or_username: user.username,
      password: user.password,
      remember_me: false,
    },
  });
  expect(login.ok(), `manager login: ${login.status()}`).toBeTruthy();
  return { context, page: await context.newPage() };
}

test.describe('Email mailbox intake routing (WI-1644)', () => {
  test.describe.configure({ mode: 'serial' });

  test('admin routes a mailbox intake to a portal; the portal lists the mailbox and the link persists', async ({
    page,
    request,
  }) => {
    const suffix = `${Date.now()}-${Math.floor(Math.random() * 1_000_000)}`;
    const portal = await createPortalChannel(request, {
      slug: `connected-portal-${suffix}`,
      name: `Connected Portal ${suffix}`,
    });
    const email = await createUnconnectedEmailChannel(request, suffix);

    await openEmailChannelSettings(page, email.channelId);
    const intakeId = await addPortalIntake(page, portal, 'INBOX');

    // The intake persists across a reload of the settings form.
    await openEmailChannelSettings(page, email.channelId);
    await expect(page.getByTestId(`channel-intake-${intakeId}`)).toBeVisible();
    await expect(page.getByTestId(`channel-intake-${intakeId}`)).toContainText('INBOX');

    // The portal's settings page lists the connected mailbox read-only.
    await page.goto(`/admin/channels/${portal.channelId}/portal`);
    await expect(page.getByTestId(`portal-connected-mailbox-${email.channelId}`)).toBeVisible();
    await expect(
      page.getByTestId(`portal-connected-mailbox-${email.channelId}`)
    ).toContainText(email.name);
    await expect(
      page.getByTestId(`portal-connected-mailbox-status-${email.channelId}`)
    ).toContainText('Disabled');

    // Removing the intake clears the link and empties the portal's mailbox list.
    await openEmailChannelSettings(page, email.channelId);
    await deleteIntake(page, intakeId);

    await page.goto(`/admin/channels/${portal.channelId}/portal`);
    await expect(page.getByTestId('portal-connected-mailboxes')).toBeVisible();
    await expect(page.getByTestId('portal-connected-mailbox-list')).toHaveCount(0);
  });

  test('saving an intake after the target access is revoked is forbidden and leaves the modal usable', async ({
    browser,
    page,
    request,
  }) => {
    const suffix = `${Date.now()}-${Math.floor(Math.random() * 1_000_000)}`;
    const portal = await createPortalChannel(request, {
      slug: `denied-portal-${suffix}`,
      name: `Denied Portal ${suffix}`,
    });
    const email = await createUnconnectedEmailChannel(request, suffix);

    const data = generateUser(suffix);
    const user = await createUserViaAPI(request, data);
    for (const channelID of [email.channelId, portal.channelId]) {
      const assign = await request.post(`/api/channels/${channelID}/managers`, {
        headers: SEC_FETCH,
        data: { manager_type: 'user', manager_ids: [user.id] },
      });
      expect(assign.ok(), `assign manager on ${channelID}: ${assign.status()}`).toBeTruthy();
    }

    const manager = await browserForUser(browser, {
      username: data.username,
      password: data.password_hash,
    });
    const pageErrors: string[] = [];
    manager.page.on('pageerror', (e) => pageErrors.push(e.message));

    // The manager manages both channels, so the portal is offered. Admin then
    // revokes the portal management while the form is open; the still-listed
    // option must not survive save-time authorization.
    await openEmailChannelSettings(manager.page, email.channelId);

    const managers = await request.get(`/api/channels/${portal.channelId}/managers`, {
      headers: SEC_FETCH,
    });
    expect(managers.ok(), `list portal managers: ${managers.status()}`).toBeTruthy();
    const rows = (await managers.json()) as Array<{
      id: number;
      manager_type: string;
      manager_id: number;
    }>;
    const row = rows.find((r) => r.manager_type === 'user' && r.manager_id === user.id);
    expect(row, 'portal manager row for the test user').toBeTruthy();
    const revoke = await request.delete(
      `/api/channels/${portal.channelId}/managers/${row!.id}`,
      { headers: SEC_FETCH }
    );
    expect(revoke.ok(), `revoke portal manager: ${revoke.status()}`).toBeTruthy();

    await manager.page.getByTestId('channel-intake-add').click();
    await manager.page.getByTestId('channel-intake-folder').fill('Denied');
    await selectPortalTarget(manager.page, portal);
    const denied = manager.page.waitForResponse(
      (resp) =>
        /\/channels\/\d+\/intakes$/.test(new URL(resp.url()).pathname) &&
        resp.request().method() === 'POST'
    );
    await manager.page.getByTestId('channel-intake-save').click();
    const deniedResponse = await denied;
    expect(deniedResponse.status(), 'stale portal target must be forbidden').toBe(403);
    await expect(manager.page.getByTestId('toast-message-error')).toContainText(
      'Insufficient permissions'
    );

    // The modal stays usable: cancel the rejected form and confirm the intake
    // list still renders without an unhandled render error.
    await expect(manager.page.getByTestId('channel-intake-save')).toBeVisible();
    expect(pageErrors, 'no unhandled render error after the failed save').toEqual([]);

    // The denial is durable: no intake was stored. Reload to prove it.
    await openEmailChannelSettings(manager.page, email.channelId);
    await expect(manager.page.getByTestId('channel-intake-list')).not.toContainText('Denied');

    await manager.context.close();
  });

  test('enabling a mailbox without an intake surfaces the server error above the modal', async ({
    page,
    request,
  }) => {
    const suffix = `${Date.now()}-${Math.floor(Math.random() * 1_000_000)}`;
    const email = await createUnconnectedEmailChannel(request, suffix);

    await openEmailChannelSettings(page, email.channelId);
    await page.getByTestId('channel-email-enable').click();

    const toggled = page.waitForResponse(
      (resp) =>
        resp.url().endsWith(`/channels/${email.channelId}/toggle`) &&
        resp.request().method() === 'PUT'
    );
    await page.getByTestId('dialog-confirm').click();
    const response = await toggled;
    expect(response.status(), 'enabling without an intake is rejected').toBe(400);

    const toast = page.getByTestId('toast-message-error').first();
    await expect(toast).toBeVisible();
    await expect(toast).toContainText('add at least one intake');

    // The modal backdrop is portaled to <body> at z-50; the shared toast
    // viewport layers above it, so the message must not be painted over.
    const topmost = await toast.evaluate((el) => {
      const rect = el.getBoundingClientRect();
      const hit = document.elementFromPoint(
        rect.left + rect.width / 2,
        rect.top + rect.height / 2
      );
      return hit === el || el.contains(hit);
    });
    expect(topmost, 'toast is not occluded by the modal backdrop').toBe(true);
  });
});
