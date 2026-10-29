import { authenticateAdminRequest } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/mail';
import {
  attachRequestTypesToSection,
  createPortalChannel,
  createSimpleRequestType,
  signInViaMagicLink,
} from '../helpers/portal-setup';

/**
 * Portal request attachment journey (WI-1130).
 *
 * A signed-in customer submits a request with a staged file, sees it in the
 * request timeline, replies with another attachment, and downloads both.
 * A second customer on the same portal gets 404s on the first customer's
 * request detail and attachment download, and an anonymous browser never
 * sees request data at all.
 *
 * Complements tests/portal_request_attachments_test.go which pins the same
 * contracts at the HTTP layer; this spec proves the browser-visible path.
 * Skips when Mailpit is unavailable.
 */

const BASE_ORIGIN = process.env.BASE_ORIGIN || 'http://localhost:8080';

// 1x1 transparent PNG.
const PNG_BASE64 =
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==';

function pngBuffer() {
  return Buffer.from(PNG_BASE64, 'base64');
}

test.describe('Portal request attachments', () => {
  test('customer attaches files on submit and reply, downloads from the timeline', {
    tag: '@critical-browser',
  }, async ({ request, mail, page }) => {
    mail.skipIfMissing();
    await authenticateAdminRequest(request);

    const stamp = Date.now();
    const slug = `e2e-attach-${stamp}`;
    const ownerEmail = `e2e-attach-owner-${stamp}@windshift.test`;
    const title = `Attachment journey ${stamp}`;

    const channel = await createPortalChannel(request, {
      slug,
      name: `Attachments ${stamp}`,
    });
    const rt = await createSimpleRequestType(request, channel, {
      name: 'General request',
    });
    await attachRequestTypesToSection(request, channel, [rt.id]);

    // Customer signs in through the portal login modal (magic link).
    await page.context().clearCookies();
    await page.goto(`/portal/${slug}`);
    await page.locator('#portal-sign-in').click();
    await signInViaMagicLink(page, { mail, slug, email: ownerEmail });
    await expect(page.getByTestId('portal-page')).toHaveAttribute('data-ready', 'true');

    // Open the request form, fill it, and stage a file on the last step.
    const requestTypeCard = page.getByTestId('portal-request-type-card');
    await expect(requestTypeCard).toBeVisible({ timeout: 10000 });
    await requestTypeCard.click();
    await expect(page.locator('#request-title')).toBeVisible({ timeout: 5000 });
    await page.locator('#request-title').fill(title);
    await page.locator('#request-description').fill('submitted with an attachment');

    await page.locator('#request-form-attachment-input').setInputFiles({
      name: 'submit-evidence.png',
      mimeType: 'image/png',
      buffer: pngBuffer(),
    });

    // Attach both listeners before the click: the staged-file upload races
    // the submit response and both must be observed.
    const submitPromise = page.waitForResponse(
      (resp) =>
        resp.url().includes(`/api/portal/${slug}/submit`) && resp.request().method() === 'POST'
    );
    const uploadPromise = page.waitForResponse(
      (resp) =>
        resp.url().includes('/attachments') && resp.request().method() === 'POST'
    );
    await page.getByTestId('request-form-submit').click();

    const submitResp = await submitPromise;
    expect(submitResp.ok(), `submit: ${submitResp.status()}`).toBeTruthy();
    const submitBody = await submitResp.json();
    const itemId: number = submitBody.id ?? submitBody.item_id ?? submitBody.item?.id;
    expect(itemId, 'submit response missing item id').toBeGreaterThan(0);

    const uploadResp = await uploadPromise;
    expect(uploadResp.status(), `attachment upload: ${uploadResp.status()}`).toBe(201);

    // The request detail shows the staged attachment.
    await expect(page).toHaveURL(new RegExp(`[?&]view=requests(?:&id=${itemId})?`), {
      timeout: 10000,
    });
    await expect(page.getByTestId('portal-request-detail-title')).toHaveText(title, {
      timeout: 5000,
    });
    await expect(page.getByTestId('portal-request-attachments')).toBeVisible({ timeout: 5000 });
    const firstAttachment = page
      .getByTestId('portal-request-attachment')
      .filter({ hasText: 'submit-evidence.png' });
    await expect(firstAttachment).toHaveCount(1);

    // The download link serves the uploaded bytes through the portal session.
    const downloadHref = await firstAttachment.getAttribute('href');
    expect(downloadHref, 'attachment link missing href').toBeTruthy();
    const downloaded = await page.request.get(downloadHref!);
    expect(downloaded.status()).toBe(200);
    expect(await downloaded.body()).toEqual(pngBuffer());

    // Selecting a reply file uploads it immediately; the comment submit only
    // posts the text. Attach the upload listener before picking the file.
    const replyUploadPromise = page.waitForResponse(
      (resp) =>
        resp.url().includes(`/portal/${slug}/requests/${itemId}/attachments`) &&
        resp.request().method() === 'POST'
    );
    await page.locator('#portal-request-attachment-input').setInputFiles({
      name: 'reply-log.txt',
      mimeType: 'text/plain',
      buffer: Buffer.from('attach-from-reply', 'utf8'),
    });
    expect((await replyUploadPromise).status()).toBe(201);

    await page.locator('#portal-request-comment').fill('Here is the extra log file');
    await page.getByTestId('portal-request-comment-submit').click();

    await expect(
      page.getByTestId('portal-request-attachment').filter({ hasText: 'reply-log.txt' })
    ).toHaveCount(1, { timeout: 5000 });
    await expect(page.getByTestId('portal-request-attachment')).toHaveCount(2);
  });

  test('another customer cannot open the request or download its attachments', async ({
    request,
    mail,
    browser,
  }) => {
    mail.skipIfMissing();
    await authenticateAdminRequest(request);

    const stamp = Date.now();
    const slug = `e2e-attach-deny-${stamp}`;
    const ownerEmail = `e2e-attach-denied-owner-${stamp}@windshift.test`;
    const otherEmail = `e2e-attach-other-${stamp}@windshift.test`;

    const channel = await createPortalChannel(request, {
      slug,
      name: `Attach deny ${stamp}`,
    });
    const rt = await createSimpleRequestType(request, channel, {
      name: 'General request',
    });
    await attachRequestTypesToSection(request, channel, [rt.id]);

    // The owner signs in and submits a request with one attachment. Open
    // portals auto-grant the customer on first submission.
    const ownerContext = await browser.newContext({ baseURL: BASE_ORIGIN, storageState: { cookies: [], origins: [] } });
    const ownerPage = await ownerContext.newPage();
    await ownerPage.goto(`/portal/${slug}`);
    await ownerPage.locator('#portal-sign-in').click();
    await signInViaMagicLink(ownerPage, { mail, slug, email: ownerEmail });
    await expect(ownerPage.getByTestId('portal-request-type-card')).toBeVisible({ timeout: 10000 });
    await ownerPage.getByTestId('portal-request-type-card').click();
    await expect(ownerPage.locator('#request-title')).toBeVisible({ timeout: 5000 });
    await ownerPage.locator('#request-title').fill(`Denied request ${stamp}`);

    const submitPromise = ownerPage.waitForResponse(
      (resp) =>
        resp.url().includes(`/api/portal/${slug}/submit`) && resp.request().method() === 'POST'
    );
    await ownerPage.getByTestId('request-form-submit').click();
    const itemId = (await (await submitPromise).json()).item_id;
    expect(itemId).toBeGreaterThan(0);

    // Setup-only upload through the owner's portal session (API is allowed
    // for setup; the browser-visible download assertions follow below).
    const attachmentResponse = await ownerContext.request.post(
      `/api/portal/${slug}/requests/${itemId}/attachments`,
      {
        multipart: {
          file: {
            name: 'owner-only.png',
            mimeType: 'image/png',
            buffer: pngBuffer(),
          },
        },
      }
    );
    expect(attachmentResponse.status()).toBe(201);
    const attachmentId = (await attachmentResponse.json()).attachment.id;
    expect(attachmentId).toBeGreaterThan(0);

    // The owner still sees their own attachment in the timeline.
    await ownerPage.goto(`/portal/${slug}?view=requests&id=${itemId}`);
    await expect(ownerPage.getByTestId('portal-request-detail-title')).toHaveText(
      `Denied request ${stamp}`,
      { timeout: 5000 }
    );
    await expect(
      ownerPage.getByTestId('portal-request-attachment').filter({ hasText: 'owner-only.png' })
    ).toHaveCount(1, { timeout: 5000 });

    // A second customer direct-navigates to the owner's request: the detail
    // API 404s and the timeline never renders the foreign request.
    const otherContext = await browser.newContext({ baseURL: BASE_ORIGIN, storageState: { cookies: [], origins: [] } });
    const otherPage = await otherContext.newPage();
    await otherPage.goto(`/portal/${slug}`);
    await otherPage.locator('#portal-sign-in').click();
    await signInViaMagicLink(otherPage, { mail, slug, email: otherEmail });
    await expect(otherPage.getByTestId('portal-page')).toHaveAttribute('data-ready', 'true');

    const detailResponse = await otherContext.request.get(`/api/portal/${slug}/requests/${itemId}`);
    expect(detailResponse.status()).toBe(404);

    await otherPage.goto(`/portal/${slug}?view=requests&id=${itemId}`);
    await expect(otherPage.getByTestId('portal-request-detail-title')).toHaveCount(0);

    const downloadResponse = await otherContext.request.get(
      `/api/portal/${slug}/requests/${itemId}/attachments/${attachmentId}/download`
    );
    expect(downloadResponse.status()).toBe(404);

    // An anonymous browser never sees the request either — the portal
    // surfaces the sign-in prompt instead of any request data.
    const anonContext = await browser.newContext({ baseURL: BASE_ORIGIN, storageState: { cookies: [], origins: [] } });
    const anonPage = await anonContext.newPage();
    await anonPage.goto(`/portal/${slug}?view=requests&id=${itemId}`);
    await expect(anonPage.locator('#portal-sign-in')).toBeVisible({ timeout: 10000 });
    await expect(anonPage.getByTestId('portal-request-detail-title')).toHaveCount(0);

    await ownerContext.close();
    await otherContext.close();
    await anonContext.close();
  });
});
