import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/mail';
import { generateWorkspace } from '../fixtures/test-data';

/**
 * Portal magic-link verify failures + recovery UX.
 *
 * Backs the change that bumped the regular sign-in TTL to 30 min and gave
 * approval-requested links a 24 h TTL, with the verify endpoint surfacing
 * a stable `code` + `email` hint so the frontend can recover from an
 * already-used or expired link without a dead-end.
 *
 * We don't simulate a literally-expired token (would need DB-clock surgery);
 * the verify-endpoint contract is identical for `expired` and `used`, so
 * the browser recovery flow exercises the used branch. The exact HTTP body
 * contract is covered by tests/e2e_security_contracts_test.go.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

interface ChannelConfig {
  name: string;
  slug: string;
}

async function createPortalChannel(
  request: import('@playwright/test').APIRequestContext,
  cfg: ChannelConfig
): Promise<void> {
  const workspace = await createWorkspaceViaAPI(request, generateWorkspace(cfg.slug));
  const resp = await request.post('/api/channels', {
    headers: SEC_FETCH,
    data: {
      name: cfg.name,
      type: 'portal',
      direction: 'inbound',
      status: 'disabled',
      slug: cfg.slug,
    },
  });
  expect(resp.ok(), `create portal channel: ${resp.status()} ${await resp.text()}`).toBeTruthy();
  const channel = await resp.json();
  const configResp = await request.put(`/api/channels/${channel.id}/config`, {
    headers: SEC_FETCH,
    data: {
      config: {
        portal_slug: cfg.slug,
        portal_workspace_ids: [workspace.id],
        portal_title: cfg.name,
        portal_registration_mode: 'open',
      },
    },
  });
  expect(
    configResp.ok(),
    `configure portal channel: ${configResp.status()} ${await configResp.text()}`
  ).toBeTruthy();
  const toggleResp = await request.put(`/api/channels/${channel.id}/toggle`, {
    headers: SEC_FETCH,
  });
  expect(
    toggleResp.ok(),
    `enable portal channel: ${toggleResp.status()} ${await toggleResp.text()}`
  ).toBeTruthy();
}

async function requestAndExtractToken(
  ctx: import('@playwright/test').APIRequestContext,
  mail: any,
  slug: string,
  email: string
): Promise<string> {
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
  return tokenMatch[1];
}

test.describe('Portal magic-link verify recovery', () => {
  test('used-link UI opens sign-in modal with prefilled email and resumes the intended destination', async ({
    request,
    mail,
    page,
  }) => {
    mail.skipIfMissing();

    const stamp = Date.now();
    const slug = `e2e-mlrui-${stamp}`;
    const customerEmail = `e2e-mlrui-${stamp}@windshift.test`;

    await createPortalChannel(request, { name: `MLR-UI ${stamp}`, slug });

    // Use page.request so the verify happens in the same context as the
    // browser navigation that follows. clearCookies first to avoid carrying
    // any admin session into the portal context.
    await page.context().clearCookies();

    const token = await requestAndExtractToken(page.request, mail, slug, customerEmail);

    // Burn the token via the API. The token row stays in the DB but is now
    // marked used — verify on it returns code:used (same recovery path as
    // an expired token from the FE's perspective).
    const burn = await page.request.get(
      `/api/portal/${slug}/auth/verify?token=${encodeURIComponent(token)}`,
      { headers: SEC_FETCH }
    );
    expect(burn.ok()).toBeTruthy();
    // The success leaves a portal session cookie on the context. Clear it so
    // the page navigation below behaves like an anonymous customer clicking
    // an old link from email.
    await page.context().clearCookies();

    const nextPath = `/portal/${slug}?view=approvals&id=12345`;
    const verifyURL = `${BASE_URL}/portal/${slug}/verify#token=${encodeURIComponent(
      token
    )}&next=${encodeURIComponent(nextPath)}`;

    await page.goto(verifyURL);

    // Login modal opens — assert via the email input which is unique to it.
    const emailInput = page.locator('#email');
    await expect(emailInput).toBeVisible({ timeout: 5000 });
    await expect(emailInput).toHaveValue(customerEmail);

    // Request a fresh link through the visible recovery form, then follow it
    // as the customer would from their email. The final URL is the user-facing
    // contract for preserving the original approval destination.
    const since = new Date();
    const requestResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/portal/${slug}/auth/request`) &&
        response.request().method() === 'POST' &&
        response.ok()
    );
    await page.getByTestId('portal-login-request-magic-link').click();
    await requestResponse;
    const message = await mail.waitForLast({
      to: customerEmail,
      subject: 'Sign in to your portal',
      since,
      timeoutMs: 5000,
    });
    const freshTokenMatch = message.Text.match(/[?#&]token=([A-Za-z0-9_=-]+)/);
    if (!freshTokenMatch) {
      throw new Error(`fresh token not found in body: ${message.Text.slice(0, 200)}`);
    }

    await page.goto(
      `${BASE_URL}/portal/${slug}/verify#token=${encodeURIComponent(freshTokenMatch[1])}`
    );
    // The customer-facing contract is that verification resumes the original
    // destination. Do not couple this browser test to the transport request;
    // the route assertion also waits through the visible verification state.
    await expect(page).toHaveURL(
      new RegExp(`${nextPath.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}$`),
      {
        timeout: 30_000,
      }
    );
  });
});
