import { authenticateAdminRequest, createItemViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/mail';
import {
  attachRequestTypesToSection,
  createPortalChannel,
  createSimpleRequestType,
  signInViaMagicLink,
} from '../helpers/portal-setup';

/**
 * Attachment visibility toggle (WI-2032).
 *
 * An editor opens a customer request, hides a file from the customer with the
 * visibility icon next to delete, and the portal timeline stops offering it.
 * The toggle is offered only on customer requests, so a plain agent-created
 * item shows no visibility affordance.
 *
 * Complements tests/attachment_visibility_toggle_test.go which pins the HTTP
 * contract; this spec proves the browser-visible path.
 */

const BASE_ORIGIN = process.env.BASE_ORIGIN || 'http://localhost:8080';

// 1x1 transparent PNG.
const PNG_BASE64 =
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==';

function pngBuffer() {
  return Buffer.from(PNG_BASE64, 'base64');
}

test.describe('Attachment visibility toggle', () => {
  test('editor hides a customer attachment and the portal stops offering it', {
    tag: '@critical-browser',
  }, async ({ request, mail, page, browser }) => {
    mail.skipIfMissing();
    await authenticateAdminRequest(request);

    const stamp = Date.now();
    const slug = `e2e-vis-${stamp}`;
    const ownerEmail = `e2e-vis-owner-${stamp}@windshift.test`;
    const title = `Visibility journey ${stamp}`;

    const channel = await createPortalChannel(request, {
      slug,
      name: `Visibility ${stamp}`,
    });
    const rt = await createSimpleRequestType(request, channel, { name: 'General request' });
    await attachRequestTypesToSection(request, channel, [rt.id]);

    // Customer submits a request with one attachment.
    const customerContext = await browser.newContext({
      baseURL: BASE_ORIGIN,
      storageState: { cookies: [], origins: [] },
    });
    const customerPage = await customerContext.newPage();
    await customerPage.goto(`/portal/${slug}`);
    await customerPage.locator('#portal-sign-in').click();
    await signInViaMagicLink(customerPage, { mail, slug, email: ownerEmail });
    await expect(customerPage.getByTestId('portal-request-type-card')).toBeVisible({
      timeout: 10_000,
    });
    await customerPage.getByTestId('portal-request-type-card').click();
    await expect(customerPage.locator('#request-title')).toBeVisible({ timeout: 5000 });
    await customerPage.locator('#request-title').fill(title);
    await customerPage.locator('#request-form-attachment-input').setInputFiles({
      name: 'evidence.png',
      mimeType: 'image/png',
      buffer: pngBuffer(),
    });

    const submitPromise = customerPage.waitForResponse(
      (resp) =>
        resp.url().includes(`/api/portal/${slug}/submit`) && resp.request().method() === 'POST'
    );
    const uploadPromise = customerPage.waitForResponse(
      (resp) => resp.url().includes('/attachments') && resp.request().method() === 'POST'
    );
    await customerPage.getByTestId('request-form-submit').click();
    const itemId = (await (await submitPromise).json()).item_id;
    await uploadPromise;
    expect(itemId).toBeGreaterThan(0);

    // The customer sees the staged file in the request timeline.
    await expect(customerPage).toHaveURL(new RegExp(`[?&]view=requests(?:&id=${itemId})?`), {
      timeout: 10_000,
    });
    const customerAttachment = customerPage.getByTestId('portal-request-attachment');
    await expect(customerAttachment).toHaveCount(1, { timeout: 5000 });

    // Admin resolves the attachment id through the production API.
    const attachmentsResp = await request.get(`/api/v2/items/${itemId}/attachments`);
    expect(attachmentsResp.ok()).toBeTruthy();
    const attachmentId = (await attachmentsResp.json()).data[0].id;
    expect(attachmentId).toBeGreaterThan(0);

    // Admin opens the item detail; the visibility toggle is offered.
    await page.goto(`/workspaces/${channel.workspaceId}/items/${itemId}`);
    await expect(page.getByTestId('item-detail-ready')).toBeVisible({ timeout: 15_000 });
    const toggle = page.getByTestId(`item-attachment-visibility-${attachmentId}`);
    await expect(toggle).toBeVisible({ timeout: 10_000 });
    await expect(toggle).toHaveAttribute('aria-label', 'Hide from customer');

    // Hiding the file flips the affordance to reveal.
    const hidePromise = page.waitForResponse(
      (resp) =>
        resp.url().includes(`/api/v2/attachments/${attachmentId}`) &&
        resp.request().method() === 'PATCH'
    );
    await toggle.click();
    const hideResp = await hidePromise;
    expect(hideResp.ok(), `hide: ${hideResp.status()}`).toBeTruthy();
    expect((await hideResp.json()).data.is_internal).toBe(true);
    await expect(toggle).toHaveAttribute('aria-label', 'Show to customer');

    // The portal timeline no longer offers the file.
    await customerPage.reload();
    await expect(customerPage.getByTestId('portal-request-detail-title')).toHaveText(title, {
      timeout: 5000,
    });
    await expect(
      customerPage.getByTestId('portal-request-attachment')
    ).toHaveCount(0);

    // Revealing it brings the file back.
    const revealPromise = page.waitForResponse(
      (resp) =>
        resp.url().includes(`/api/v2/attachments/${attachmentId}`) &&
        resp.request().method() === 'PATCH'
    );
    await toggle.click();
    const revealResp = await revealPromise;
    expect(revealResp.ok(), `reveal: ${revealResp.status()}`).toBeTruthy();
    expect((await revealResp.json()).data.is_internal).toBe(false);
    await expect(toggle).toHaveAttribute('aria-label', 'Hide from customer');

    await customerPage.reload();
    await expect(
      customerPage.getByTestId('portal-request-attachment')
    ).toHaveCount(1, { timeout: 5000 });

    // A plain agent-created item has no portal audience, so it shows no
    // visibility affordance even for an editor.
    const agentItem = await createItemViaAPI(request, channel.workspaceId, {
      title: `Internal item ${stamp}`,
    });
    const agentUpload = await request.post(
      `/api/v2/items/${agentItem.id}/attachments`,
      {
        multipart: {
          file: {
            name: 'internal.png',
            mimeType: 'image/png',
            buffer: pngBuffer(),
          },
        },
      }
    );
    expect(agentUpload.ok()).toBeTruthy();
    const agentAttachmentId = (await agentUpload.json()).data.id;

    await page.goto(`/workspaces/${channel.workspaceId}/items/${agentItem.id}`);
    await expect(page.getByTestId('item-detail-ready')).toBeVisible({ timeout: 15_000 });
    await expect(page.getByTestId('item-attachment')).toHaveCount(1, { timeout: 10_000 });
    await expect(
      page.getByTestId(`item-attachment-visibility-${agentAttachmentId}`)
    ).toHaveCount(0);

    await customerContext.close();
  });
});
