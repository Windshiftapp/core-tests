import { expect, test } from '../fixtures/mail';
import { createPortalChannel } from '../helpers/portal-setup';

/**
 * Portal magic-link channel binding.
 *
 * Regression coverage for the fix in portal_auth.go:VerifyMagicLink — a
 * token issued for portal A must not be redeemable via portal B's verify
 * endpoint. Before the fix, the slug's channel and the token's channel
 * were both resolved but never compared, so any portal session could be
 * minted from any leaked token, breaking channel isolation.
 *
 * We send a real magic-link email from portal A, extract the token from
 * Mailpit, then drive the browser to portal B's /verify#token=... URL and
 * assert the error UI lands instead of a session.
 *
 * Skips when Mailpit is unavailable (same pattern as the other portal
 * specs).
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

test.describe('Portal magic-link channel binding', () => {
  test('token issued for portal A is rejected at portal B /verify', async ({
    request,
    mail,
    page,
  }) => {
    mail.skipIfMissing();

    const stamp = Date.now();
    const slugA = `e2e-mlcb-a-${stamp}`;
    const slugB = `e2e-mlcb-b-${stamp}`;
    const customerEmail = `e2e-mlcb-${stamp}@windshift.test`;

    // Two distinct portal channels — the binding fix prevents cross-redemption.
    await createPortalChannel(request, { slug: slugA, name: `MLCB A ${stamp}` });
    await createPortalChannel(request, { slug: slugB, name: `MLCB B ${stamp}` });

    // Use page.request so the verify happens in the same browser context.
    // clearCookies first to avoid carrying the admin session into the portal
    // navigation that follows — otherwise PortalVerifyLink might be skipped
    // because /auth/me already returns an internal session.
    await page.context().clearCookies();

    // Request a magic link for portal A and pull the token from Mailpit.
    const since = new Date();
    const reqResp = await page.request.post(`/api/portal/${slugA}/auth/request`, {
      headers: SEC_FETCH,
      data: { email: customerEmail },
    });
    expect(reqResp.ok(), `magic-link request to portal A: ${reqResp.status()}`).toBeTruthy();

    const msg = await mail.waitForLast({
      to: customerEmail,
      subject: 'Sign in to your portal',
      since,
      timeoutMs: 5000,
    });
    const tokenMatch = msg.Text.match(/[?#&]token=([A-Za-z0-9_=-]+)/);
    if (!tokenMatch) {
      throw new Error(`token not found in body: ${msg.Text.slice(0, 200)}`);
    }
    const token = tokenMatch[1];

    // Try to redeem the token via portal B's verify endpoint. The frontend
    // verify URL puts the token in the URL fragment (#token=...). The
    // PortalVerifyLink component reads it and calls the /verify API.
    //
    // Magic-link tokens are single-use, so we capture the response from the
    // FIRST verify call (the one the page makes on load). A second hit on
    // the same token would return code:used regardless of channel binding.
    const verifyURL = `${BASE_URL}/portal/${slugB}/verify#token=${encodeURIComponent(token)}`;
    const verifyResponsePromise = page.waitForResponse(
      (resp) =>
        resp.url().includes(`/api/portal/${slugB}/auth/verify`) && resp.request().method() === 'GET'
    );
    await page.goto(verifyURL);
    const verifyResp = await verifyResponsePromise;
    expect(verifyResp.status(), `mismatched-channel verify must be 401`).toBe(401);
    const verifyBody = await verifyResp.json();
    expect(verifyBody.success).toBe(false);
    expect(verifyBody.code, 'expected code:invalid for channel mismatch (not used/expired)').toBe(
      'invalid'
    );

    // Error UI lands. The container has data-testid to keep the assertion
    // stable across localized copy.
    await expect(page.locator('[data-testid="portal-verify-error"]')).toBeVisible({
      timeout: 5000,
    });

    // And the customer is not signed in on portal B.
    const meResp = await page.request.get(`/api/portal/${slugB}/auth/me`, {
      headers: SEC_FETCH,
    });
    const meBody = await meResp.json();
    expect(meBody.authenticated, 'customer must not be signed in on portal B').toBe(false);
  });
});
