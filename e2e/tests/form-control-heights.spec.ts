import { expect, test, type APIRequestContext, type Page } from '../fixtures/context-path';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

/**
 * Create a disabled outbound SMTP channel so its configuration form renders.
 */
async function createSmtpChannel(admin: APIRequestContext, suffix: string): Promise<number> {
  const create = await admin.post('/api/channels', {
    headers: SEC_FETCH,
    data: {
      name: `SMTP Mailer ${suffix}`,
      type: 'smtp',
      direction: 'outbound',
      status: 'disabled',
    },
  });
  expect(create.ok(), `create smtp channel: ${create.status()} ${await create.text()}`).toBeTruthy();
  const channel = await create.json();

  const config = await admin.put(`/api/channels/${channel.id}/config`, {
    headers: SEC_FETCH,
    data: {
      config: {
        smtp_host: `smtp-${suffix}.example.com`,
        smtp_port: 587,
        smtp_username: `mailer-${suffix}@example.com`,
        smtp_from_email: `noreply-${suffix}@example.com`,
        smtp_from_name: 'Windshift',
        smtp_encryption: 'tls',
        smtp_skip_tls_verify: false,
      },
    },
  });
  expect(config.ok(), `configure smtp channel: ${config.status()} ${await config.text()}`).toBeTruthy();

  return channel.id;
}

async function openChannelSettings(page: Page, channelId: number): Promise<void> {
  await page.goto('/admin/channels');
  const row = page.getByTestId(`admin-channel-row-${channelId}`);
  await expect(row).toBeVisible();
  await row.click();
}

test('text inputs and selects render at the same height', async ({ request, page }) => {
  const channelId = await createSmtpChannel(request, `${process.pid}-${Date.now()}`);
  await openChannelSettings(page, channelId);

  const portInput = page.getByTestId('smtp-port');
  const encryptionSelect = page.locator('#smtp-encryption');
  await expect(portInput).toBeVisible();
  await expect(encryptionSelect).toBeVisible();

  const portBox = await portInput.boundingBox();
  const encryptionBox = await encryptionSelect.boundingBox();
  if (!portBox || !encryptionBox) {
    throw new Error('SMTP port input and encryption select should be visible');
  }

  expect(portBox.height).toBe(encryptionBox.height);
});
