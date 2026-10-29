import { expect, test } from '../fixtures/context-path';
import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { generateWorkspace } from '../fixtures/test-data';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

// The monitored mailbox is admin-entered configuration since GH #363: the
// OAuth callback no longer reads an identity from the token, so the flow must
// not start until the address is present.
test.describe('Email channel OAuth mailbox address', () => {
  test('connecting is blocked until the monitored mailbox is entered', async ({ page, request }) => {
    const suffix = `${Date.now()}-${Math.floor(Math.random() * 1_000_000)}`;
    await createWorkspaceViaAPI(request, generateWorkspace(`oauth-mailbox-${suffix}`));

    const create = await request.post('/api/channels', {
      headers: SEC_FETCH,
      data: {
        name: `OAuth Mailbox ${suffix}`,
        type: 'email',
        direction: 'inbound',
        status: 'disabled',
      },
    });
    expect(create.ok(), `create email channel: ${create.status()} ${await create.text()}`).toBeTruthy();
    const channel = await create.json();

    const config = await request.put(`/api/channels/${channel.id}/config`, {
      headers: SEC_FETCH,
      data: {
        config: {
          email_auth_method: 'oauth',
          email_oauth_provider_type: 'microsoft',
          email_oauth_client_id: 'test-client-id',
          email_oauth_client_secret: 'test-client-secret',
          email_oauth_tenant_id: 'common',
        },
      },
    });
    expect(config.ok(), `configure email channel: ${config.status()} ${await config.text()}`).toBeTruthy();

    // A blocked connect must not reach the OAuth start endpoint.
    let startCalls = 0;
    page.on('request', (req) => {
      if (req.url().includes(`/channels/${channel.id}/inline-oauth/start`)) startCalls += 1;
    });

    await page.goto('/admin/channels');
    await expect(page.getByTestId(`admin-channel-row-${channel.id}`)).toBeVisible();
    await page.getByTestId(`admin-channel-row-${channel.id}`).click();

    const mailbox = page.getByTestId('channel-oauth-mailbox');
    await expect(mailbox).toBeVisible();
    await expect(mailbox).toHaveValue('');

    await page.getByTestId('channel-oauth-connect').click();

    const toast = page.getByTestId('toast-message-error').first();
    await expect(toast).toBeVisible();
    await expect(toast).toContainText('mailbox address');
    expect(startCalls, 'a missing mailbox must not start the OAuth flow').toBe(0);
  });
});
