import { createGroupViaAPI, createUserViaAPI } from '../fixtures/api-helpers';
import {
  type APIRequestContext,
  type Browser,
  type BrowserContext,
  expect,
  type Page,
  test,
} from '../fixtures/context-path';
import { generateGroup, generateUser } from '../fixtures/test-data';
import { createPortalChannel, type PortalChannelHandle } from '../helpers/portal-setup';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

interface ChannelManagerBrowser {
  context: BrowserContext;
  page: Page;
}

async function expectStatus(
  response: { status: () => number; text: () => Promise<string> },
  status: number,
  label: string
): Promise<void> {
  if (response.status() !== status) {
    throw new Error(
      `${label}: expected ${status}, got ${response.status()} ${await response.text()}`
    );
  }
}

async function createUser(
  admin: APIRequestContext,
  suffix: string
): Promise<{ id: number; username: string; password: string }> {
  const data = generateUser(suffix);
  const user = await createUserViaAPI(admin, data);
  return { id: user.id, username: data.username, password: data.password_hash };
}

async function browserForUser(
  browser: Browser,
  user: { username: string; password: string }
): Promise<ChannelManagerBrowser> {
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
  await expectStatus(login, 200, `login ${user.username}`);
  return { context, page: await context.newPage() };
}

async function assignManager(
  admin: APIRequestContext,
  channelID: number,
  managerType: 'user' | 'group',
  managerID: number
): Promise<void> {
  const response = await admin.post(`/api/channels/${channelID}/managers`, {
    headers: SEC_FETCH,
    data: {
      manager_type: managerType,
      manager_ids: [managerID],
    },
  });
  expect(
    response.ok(),
    `assign ${managerType} manager: ${response.status()} ${await response.text()}`
  ).toBeTruthy();
}

async function managedPortal(
  admin: APIRequestContext,
  suffix: string
): Promise<PortalChannelHandle> {
  return createPortalChannel(admin, {
    slug: `channel-manager-${suffix}-${Date.now()}`,
    name: `Channel manager ${suffix} ${Date.now()}`,
  });
}

test.describe('Channel manager journey', () => {
  test('direct manager sees only managed channels and can update portal appearance', async ({
    request,
    browser,
    page,
  }) => {
    const manager = await createUser(request, 'channel-manager-direct');
    const managed = await managedPortal(request, 'direct');
    const unowned = await managedPortal(request, 'unowned');
    await assignManager(request, managed.channelId, 'user', manager.id);

    const allChannelsResponse = await request.get('/api/channels?include_disabled=true', {
      headers: SEC_FETCH,
    });
    await expectStatus(allChannelsResponse, 200, 'admin channel list');
    const allChannels = (await allChannelsResponse.json()) as Array<{
      id: number;
      is_default?: boolean;
    }>;
    const defaultChannelIDs = allChannels
      .filter((channel) => channel.is_default)
      .map((channel) => channel.id);

    const user = await browserForUser(browser, manager);
    try {
      await user.page.goto('/manage/channels');
      await expect(user.page.locator('#nav-channel-management')).toBeVisible();
      await expect(user.page.getByTestId(`manager-channel-row-${managed.channelId}`)).toBeVisible();
      await expect(user.page.getByTestId(`manager-channel-row-${unowned.channelId}`)).toHaveCount(
        0
      );
      for (const channelID of defaultChannelIDs) {
        await expect(user.page.getByTestId(`manager-channel-row-${channelID}`)).toHaveCount(0);
      }
      await expect(user.page.getByTestId(`channel-actions-${managed.channelId}`)).toHaveCount(0);

      await user.page.getByTestId(`manager-channel-row-${managed.channelId}`).click();
      await expect(user.page).toHaveURL(new RegExp(`/admin/channels/${managed.channelId}/portal$`));
      await expect(user.page.getByTestId('channel-tab-settings')).toBeVisible();
      await expect(user.page.getByTestId('channel-tab-managers')).toHaveCount(0);

      const updatedTitle = `Managed appearance ${Date.now()}`;
      await user.page.getByTestId('channel-portal-title').fill(updatedTitle);
      const saveResponsePromise = user.page.waitForResponse(
        (response) =>
          response.url().endsWith(`/api/channels/${managed.channelId}/config`) &&
          response.request().method() === 'PUT'
      );
      await user.page.getByTestId('channel-save').click();
      const saveResponse = await saveResponsePromise;
      expect(saveResponse.ok(), `save portal appearance: ${saveResponse.status()}`).toBeTruthy();
      await user.page.reload();
      await expect(user.page.getByTestId('channel-portal-title')).toHaveValue(updatedTitle);

      const deniedResponsePromise = user.page.waitForResponse(
        (response) =>
          response.url().endsWith(`/api/channels/${unowned.channelId}`) &&
          response.request().method() === 'GET'
      );
      await user.page.goto(`/admin/channels/${unowned.channelId}/portal`);
      const deniedResponse = await deniedResponsePromise;
      expect(deniedResponse.status()).toBe(403);
      await expect(user.page.getByTestId('toast').first()).toHaveAttribute(
        'data-toast-variant',
        'error'
      );

      await page.goto(`/admin/channels/${managed.channelId}/portal`);
      await expect(page.getByTestId('channel-tab-managers')).toBeVisible();
      await page.goto('/admin/channels');
      await page.getByTestId(`channel-actions-${managed.channelId}`).click();
      await expect(page.getByTestId('channel-delete')).toBeVisible();
    } finally {
      await user.context.close();
    }
  });

  test('user who manages no channels does not see the Channels navigation entry', async ({
    request,
    browser,
  }) => {
    const userData = await createUser(request, 'channel-manager-none');
    const user = await browserForUser(browser, userData);
    try {
      await user.page.goto('/');
      await expect(user.page.locator('#nav-channel-management')).toHaveCount(0);
    } finally {
      await user.context.close();
    }
  });

  test('active group assignment grants list and portal access', async ({ request, browser }) => {
    const manager = await createUser(request, 'channel-manager-group');
    const group = await createGroupViaAPI(
      request,
      generateGroup(`channel-manager-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`)
    );
    const addMember = await request.post(`/api/groups/${group.id}/members`, {
      headers: SEC_FETCH,
      data: { user_ids: [manager.id] },
    });
    expect(
      addMember.ok(),
      `add group member: ${addMember.status()} ${await addMember.text()}`
    ).toBeTruthy();

    const managed = await managedPortal(request, 'group');
    await assignManager(request, managed.channelId, 'group', group.id);

    const user = await browserForUser(browser, manager);
    try {
      await user.page.goto('/manage/channels');
      await expect(user.page.locator('#nav-channel-management')).toBeVisible();
      await user.page.getByTestId(`manager-channel-row-${managed.channelId}`).click();
      await expect(user.page).toHaveURL(new RegExp(`/admin/channels/${managed.channelId}/portal$`));
      await expect(user.page.getByTestId('channel-tab-settings')).toBeVisible();
    } finally {
      await user.context.close();
    }
  });
});
