import type { BrowserContext, CDPSession, Page } from '../fixtures/context-path';
import { expect, test } from '../fixtures/mail';
import { createPortalChannel } from '../helpers/portal-setup';

/**
 * Portal passkey enrolment + discoverable login.
 *
 * Drives the real flow end-to-end against a Chromium virtual authenticator
 * installed via CDP (`WebAuthn.enable` + `WebAuthn.addVirtualAuthenticator`).
 * The Svelte UI calls `navigator.credentials.create/get` as it would in a
 * real browser; the virtual authenticator transparently fulfils those calls.
 *
 * Skips when Mailpit isn't on PATH — the initial customer session is seeded
 * by redeeming a magic link, which requires email capture.
 *
 * The three steps share a single BrowserContext + Page so the portal
 * session cookie set by step 1's verify call carries into step 2's
 * registration and step 3's logout/login. Playwright's default `page`
 * fixture is per-test and inherits the admin storageState from
 * `playwright.config.ts`, which would clobber the portal session.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

async function requestAndExtractToken(
  ctx: import('@playwright/test').APIRequestContext,
  mail: any,
  slug: string,
  email: string
): Promise<{ token: string; message: any }> {
  const since = new Date();
  const reqResp = await ctx.post(`/api/portal/${slug}/auth/request`, {
    headers: SEC_FETCH,
    data: { email },
  });
  expect(reqResp.ok(), `magic-link request: ${reqResp.status()}`).toBeTruthy();

  const msg = await mail.waitForLast({
    to: email,
    subject: 'Sign in to your portal',
    since,
    timeoutMs: 5000,
  });
  const tokenMatch = msg.Text.match(/[?#&]token=([A-Za-z0-9_=-]+)/);
  if (!tokenMatch) {
    throw new Error(`token not found in body: ${msg.Text.slice(0, 200)}`);
  }
  return { token: tokenMatch[1], message: msg };
}

test.describe.configure({ mode: 'serial' });

test.describe('Portal passkey enrolment + discoverable login', () => {
  const stamp = Date.now();
  const slug = `e2e-pk-${stamp}`;
  const customerEmail = `e2e-pk-${stamp}@windshift.test`;
  const credentialName = 'E2E Authenticator';

  // Shared across all three tests — see file-level comment for why.
  let customerCtx: BrowserContext;
  let customerPage: Page;
  let cdp: CDPSession | null = null;
  let authenticatorId: string | null = null;

  test.beforeAll(async ({ browser, request }) => {
    // Admin context (default storageState) creates the portal channel.
    await createPortalChannel(request, { name: `PK ${stamp}`, slug });

    // Customer context — empty storageState avoids inheriting admin auth.
    customerCtx = await browser.newContext({ storageState: { cookies: [], origins: [] } });
    customerPage = await customerCtx.newPage();
  });

  test.afterAll(async () => {
    if (cdp && authenticatorId) {
      await cdp.send('WebAuthn.removeVirtualAuthenticator', { authenticatorId }).catch(() => {});
    }
    if (customerCtx) await customerCtx.close().catch(() => {});
  });

  test('1. magic-link sign-in shows the passkey banner', async ({ mail }) => {
    // page.request uses the same context as the page → verify's Set-Cookie
    // lands on customerCtx and carries into subsequent goto's.
    const { token } = await requestAndExtractToken(customerPage.request, mail, slug, customerEmail);
    const verify = await customerPage.request.get(
      `/api/portal/${slug}/auth/verify?token=${encodeURIComponent(token)}`,
      { headers: SEC_FETCH }
    );
    expect(verify.ok(), `verify magic link: ${verify.status()}`).toBeTruthy();

    await customerPage.goto(`/portal/${slug}`);
    await expect(customerPage.getByTestId('portal-passkey-banner')).toBeVisible({
      timeout: 10000,
    });
  });

  test('2. enrol a passkey via the Profile page', async () => {
    // Install a Chromium virtual authenticator on the shared context's
    // CDP target. The authenticator persists across goto's within the
    // same context.
    cdp = await customerCtx.newCDPSession(customerPage);
    await cdp.send('WebAuthn.enable', { enableUI: false });
    const res = await cdp.send('WebAuthn.addVirtualAuthenticator', {
      options: {
        protocol: 'ctap2',
        transport: 'internal',
        hasResidentKey: true,
        hasUserVerification: true,
        isUserVerified: true,
        automaticPresenceSimulation: true,
      },
    });
    authenticatorId = res.authenticatorId;

    await customerPage.goto(`/portal/${slug}/profile`);

    await customerPage.getByTestId('portal-add-passkey').click();
    await customerPage.locator('#passkey-name').fill(credentialName);
    await customerPage.getByTestId('portal-passkey-register-submit').click();

    // Credential lands in the list and the dialog leaves the DOM after its
    // success transition.
    await expect(
      customerPage.getByTestId('portal-passkey-list').getByText(credentialName)
    ).toBeVisible({ timeout: 10000 });
    await expect(customerPage.locator('[role="dialog"][aria-modal="true"]')).toHaveCount(0);

    await customerPage.goto(`/portal/${slug}`);
    await expect(customerPage.getByTestId('portal-passkey-banner')).toHaveCount(0);
  });

  test('3. sign in with the discoverable passkey', async () => {
    // Open the avatar dropdown, then logout via the menu item.
    await customerPage.locator('#portal-avatar-button').click();
    await customerPage.getByTestId('portal-logout').click();

    // The unauthenticated portal hero shows a "Sign in" CTA. Click it to
    // open the login modal, then trigger the passkey flow.
    const signInCta = customerPage.getByRole('button', { name: /sign in/i }).first();
    await signInCta.click();

    await expect(customerPage.locator('#email')).toBeVisible({ timeout: 5000 });
    await customerPage.getByTestId('portal-passkey-login').click();

    // After successful discoverable login, the modal closes and the
    // authenticated portal returns. Banner stays hidden because the
    // customer already has a passkey enrolled.
    await expect(customerPage.locator('#email')).toBeHidden({ timeout: 10000 });
    await expect(customerPage.locator('#portal-avatar-button')).toBeVisible();
    await expect(customerPage.getByTestId('portal-passkey-banner')).toHaveCount(0);
  });
});
