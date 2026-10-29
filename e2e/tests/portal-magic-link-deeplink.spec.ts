import { expect, test } from '../fixtures/mail';
import { authenticateAdminRequest } from '../fixtures/api-helpers';
import {
  attachRequestTypesToSection,
  createPortalChannel,
  createSimpleRequestType,
} from '../helpers/portal-setup';

/**
 * Magic-link deep-link preservation.
 *
 * A customer follows a request URL from an email while signed out, signs in
 * through the portal login modal, and the emailed verify link must carry the
 * original request parameters so they land back on the same ticket instead of
 * the portal home.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
// Origin only — the magic link already carries the context-path prefix in its
// path, so normalisation must not re-add it (BASE_URL would double it up).
const BASE_ORIGIN = process.env.BASE_ORIGIN || new URL(BASE_URL).origin;

test.describe('Portal magic-link deep-link preservation', () => {
  test('request URL followed anonymously is restored after magic-link sign-in', async ({
    page,
    request,
    mail,
    browser,
  }) => {
    mail.skipIfMissing();
    await authenticateAdminRequest(request);

    const stamp = Date.now();
    const slug = `e2e-mldl-${stamp}`;
    const customerEmail = `e2e-mldl-${stamp}@windshift.test`;
    const title = `Deep link request ${stamp}`;

    // Setup: portal channel + a single-step request type wired into a section.
    const channel = await createPortalChannel(request, { slug, name: `Deep Link ${stamp}` });
    const rt = await createSimpleRequestType(request, channel, { name: 'General request' });
    await attachRequestTypesToSection(request, channel, [rt.id]);

    // Create the ticket through the portal API as its owner so the deep link
    // has a real request to land on. This context is only used for setup.
    const ownerContext = await browser.newContext({ storageState: { cookies: [], origins: [] } });
    const ownerPage = await ownerContext.newPage();
    const ownerSince = new Date();
    const ownerRequest = await ownerPage.request.post(`/api/portal/${slug}/auth/request`, {
      headers: SEC_FETCH,
      data: { email: customerEmail },
    });
    expect(ownerRequest.ok(), `owner magic-link request: ${ownerRequest.status()}`).toBeTruthy();
    const ownerMessage = await mail.waitForLast({
      to: customerEmail,
      subject: 'Sign in to your portal',
      since: ownerSince,
      timeoutMs: 5000,
    });
    const ownerToken = ownerMessage.Text.match(/[?#&]token=([A-Za-z0-9_=-]+)/)?.[1];
    expect(ownerToken, `owner token missing: ${ownerMessage.Text.slice(0, 200)}`).toBeTruthy();
    const ownerVerify = await ownerPage.request.get(
      `/api/portal/${slug}/auth/verify?token=${encodeURIComponent(ownerToken!)}`,
      { headers: SEC_FETCH }
    );
    expect(ownerVerify.ok(), 'verify owner magic link').toBeTruthy();
    const submitResponse = await ownerPage.request.post(`/api/portal/${slug}/submit`, {
      headers: SEC_FETCH,
      data: { title, description: 'created for the deep-link preservation spec' },
    });
    expect(
      submitResponse.ok(),
      `portal submit: ${submitResponse.status()} ${await submitResponse.text()}`
    ).toBeTruthy();
    const { item_id: itemId } = (await submitResponse.json()) as { item_id: number };
    expect(itemId, 'submit response missing item id').toBeGreaterThan(0);
    await ownerContext.close();

    // Anonymous visitor follows the request URL out of their inbox.
    await page.context().clearCookies();
    const requestURL = `/portal/${slug}?view=requests&id=${itemId}`;
    await page.goto(`${BASE_URL}${requestURL}`);

    // The portal gates the ticket behind sign-in.
    await page.locator('#portal-sign-in').click();
    await expect(page.locator('#email')).toBeVisible({ timeout: 5000 });

    const since = new Date();
    await page.locator('#email').fill(customerEmail);
    await page.getByTestId('portal-login-request-magic-link').click();

    const message = await mail.waitForLast({
      to: customerEmail,
      subject: 'Sign in to your portal',
      since,
      timeoutMs: 5000,
    });
    const linkMatch = message.Text.match(/(https?:\/\/\S+\/portal\/\S+\/verify#token=[^\s>]+)/);
    expect(linkMatch, `magic-link URL missing: ${message.Text.slice(0, 200)}`).toBeTruthy();
    const link = linkMatch![1].replace(/^https?:\/\/[^/]+/, BASE_ORIGIN);

    // The emailed link must carry the request parameters verbatim.
    const fragment = new URLSearchParams(new URL(link).hash.slice(1));
    expect(fragment.get('next')).toBe(requestURL);

    const verifyPromise = page.waitForResponse(
      (resp) =>
        resp.url().includes(`/api/portal/${slug}/auth/verify`) &&
        resp.request().method() === 'GET'
    );
    await page.goto(link);
    const verify = await verifyPromise;
    expect(verify.ok(), `verify: ${verify.status()}`).toBeTruthy();

    // Landing back on the original ticket proves the parameters survived.
    await expect(page).toHaveURL(new RegExp(`view=requests&id=${itemId}`), { timeout: 10000 });
    await expect(page.getByTestId('portal-request-detail-title')).toHaveText(title, {
      timeout: 10000,
    });
  });
});
