import { authenticateAdminRequest } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/mail';
import {
  attachRequestTypesToSection,
  createPortalChannel,
  createSimpleRequestType,
} from '../helpers/portal-setup';

/**
 * Portal happy-path submission via the UI.
 *
 * Anonymous customer → click request type → login modal (gated) → request
 * magic link → verify in same browser context → form is reopened for the
 * pending request type → fill & submit → row appears in My Requests.
 *
 * Complements approvals-portal.spec.ts which covers the same flow at the
 * API level; this is the regression net for the entire UI path. Skips
 * when Mailpit is unavailable.
 */

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
// Origin only — the magic link already carries the context-path prefix in its
// path, so normalisation must not re-add it (BASE_URL would double it up).
const BASE_ORIGIN = process.env.BASE_ORIGIN || new URL(BASE_URL).origin;

test.describe('Portal happy-path submission', () => {
  test('anon → magic link → submit → row visible in My Requests', {
    tag: '@critical-browser',
  }, async ({ request, mail, page }) => {
    mail.skipIfMissing();
    await authenticateAdminRequest(request);

    const stamp = Date.now();
    const slug = `e2e-hp-${stamp}`;
    const customerEmail = `e2e-hp-${stamp}@windshift.test`;
    const submissionTitle = `E2E happy path ${stamp}`;
    const submissionDescription = 'submitted via the UI happy-path spec';

    // 1. Setup: portal channel + a single-step request type wired into a section.
    const channel = await createPortalChannel(request, {
      slug,
      name: `Happy Path ${stamp}`,
    });
    const rt = await createSimpleRequestType(request, channel.channelId, {
      name: 'General request',
    });
    await attachRequestTypesToSection(request, channel, [rt.id]);

    // 2. Strip admin cookies so the portal session manager doesn't fall back
    //    on the internal session when SubmitToPortal asks "who's the actor?".
    await page.context().clearCookies();

    // 3. Land on the portal as an anonymous user. The portal hides
    //    request-type cards entirely behind auth — Portal.svelte renders
    //    only a hero + "Sign In" prompt when isAuthenticated is false. So
    //    the click target here is the Sign In button, not the card.
    await page.goto(`${BASE_URL}/portal/${slug}`);
    await page.locator('#portal-sign-in').click();

    // 4. Login modal pops up with email + magic-link button.
    const emailInput = page.locator('#email');
    await expect(emailInput).toBeVisible({ timeout: 5000 });
    const since = new Date();
    await emailInput.fill(customerEmail);
    await page.getByTestId('portal-login-request-magic-link').click();

    // 5. Pull the magic link from Mailpit and visit it. The verify page
    //    settles, then Portal.svelte's auth-state $effect re-renders the
    //    page; the pending request type is reopened automatically.
    const msg = await mail.waitForLast({
      to: customerEmail,
      subject: 'Sign in to your portal',
      since,
      timeoutMs: 5000,
    });
    const linkMatch = msg.Text.match(/(https?:\/\/\S+\/portal\/\S+\/verify#token=[^\s>]+)/);
    if (!linkMatch) {
      throw new Error(`magic-link URL missing: ${msg.Text.slice(0, 200)}`);
    }
    const normalisedLink = linkMatch[1].replace(/^https?:\/\/[^/]+/, BASE_ORIGIN);
    const verifyPromise = page.waitForResponse(
      (resp) =>
        resp.url().includes(`/api/portal/${slug}/auth/verify`) && resp.request().method() === 'GET'
    );
    await page.goto(normalisedLink);
    const verifyResp = await verifyPromise;
    expect(verifyResp.ok(), `verify: ${verifyResp.status()}`).toBeTruthy();

    // 6. Settle on the portal home (now authenticated — Portal.svelte
    //    renders PortalSections instead of the sign-in prompt). Click the
    //    request-type card to open the form.
    await expect(page).toHaveURL(new RegExp(`/portal/${slug}/?$`), {
      timeout: 10000,
    });
    await expect(page.getByTestId('portal-verify-link')).toBeHidden();
    const requestTypeCard = page.getByTestId('portal-request-type-card');
    await expect(requestTypeCard).toBeVisible({ timeout: 10000 });
    await expect(requestTypeCard).toContainText(rt.name);
    await requestTypeCard.click();
    await expect(page.locator('#request-title')).toBeVisible({ timeout: 5000 });

    // 7. Fill + submit. Wait on the submit response to assert the backend
    //    accepted the item; the UI may close the modal asynchronously.
    await page.locator('#request-title').fill(submissionTitle);
    await page.locator('#request-description').fill(submissionDescription);

    const submitPromise = page.waitForResponse(
      (resp) =>
        resp.url().includes(`/api/portal/${slug}/submit`) && resp.request().method() === 'POST'
    );
    await page.getByTestId('request-form-submit').click();
    const submitResp = await submitPromise;
    expect(submitResp.ok(), `submit: ${submitResp.status()}`).toBeTruthy();
    const submitBody = await submitResp.json();
    const itemId: number = submitBody.id ?? submitBody.item_id ?? submitBody.item?.id;
    expect(itemId, 'submit response missing item id').toBeGreaterThan(0);

    // 8. Portal.svelte navigates to ?view=requests&id={itemId} on successful
    //    submit, which renders PortalMyRequests in its detail-pane state
    //    (the list view is hidden by an {#if selectedRequest}/else). Assert
    //    the URL carries our item id and the detail header shows our title.
    await expect(page).toHaveURL(new RegExp(`[?&]view=requests(?:&id=${itemId})?`), {
      timeout: 10000,
    });
    await expect(page.getByTestId('portal-request-detail-title')).toHaveText(submissionTitle, {
      timeout: 5000,
    });

    const requestDetails = page.getByTestId('portal-request-details');
    await expect(requestDetails).toBeVisible();
    await expect(requestDetails).toContainText('Status');
    await expect(requestDetails).toContainText('Request type');
    await expect(requestDetails).toContainText(rt.name);
    await expect(requestDetails).toContainText('Service');
    await expect(requestDetails).toContainText('Priority');
  });
});
