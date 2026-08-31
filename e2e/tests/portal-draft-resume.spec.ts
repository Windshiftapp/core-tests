import { expect, test } from '../fixtures/mail';
import {
  attachRequestTypesToSection,
  createPortalChannel,
  createTwoStepRequestType,
} from '../helpers/portal-setup';

/**
 * Portal request-form draft save & resume.
 *
 * Regression coverage for the recently-shipped draft feature
 * (internal/handlers/portal_drafts.go + portal_draft_repository.go).
 * Auto-save fires when the user advances to the next step; reopening the
 * same form should hydrate the saved title/description and land on the
 * stored current_step.
 *
 * The request type is two-step: title on step 1, description on step 2. No
 * custom field definitions are needed — splitting the two default fields
 * across steps is enough to exercise the multi-step + draft path.
 *
 * Skips when Mailpit is unavailable.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

test.describe('Portal request-form draft resume', () => {
  test('advancing a step saves a draft; reopening restores title + step', async ({
    request,
    mail,
    page,
  }) => {
    mail.skipIfMissing();

    const stamp = Date.now();
    const slug = `e2e-drft-${stamp}`;
    const customerEmail = `e2e-drft-${stamp}@windshift.test`;
    const draftTitle = `draft title ${stamp}`;

    // 1. Setup: portal channel + 2-step request type, wired into a section so
    //    the request-type card actually appears on the portal page.
    const channel = await createPortalChannel(request, {
      slug,
      name: `Draft Resume ${stamp}`,
    });
    const rt = await createTwoStepRequestType(request, channel.channelId, {
      name: 'Two-step bug report',
    });
    await attachRequestTypesToSection(request, channel, [rt.id]);

    // 2. Anonymous customer context (clearCookies discards the admin session
    //    that the project's storageState set on this page).
    await page.context().clearCookies();

    // 3. Magic-link sign-in via API → land the portal session cookie on the
    //    browser context, then navigate to the portal home. UI-driven login
    //    works too but is overkill here; the happy-path spec covers that.
    const since = new Date();
    const reqResp = await page.request.post(`/api/portal/${slug}/auth/request`, {
      headers: SEC_FETCH,
      data: { email: customerEmail },
    });
    expect(reqResp.ok(), `magic-link request: ${reqResp.status()}`).toBeTruthy();

    const msg = await mail.waitForLast({
      to: customerEmail,
      subject: 'Sign in to your portal',
      since,
      timeoutMs: 5000,
    });
    const tokenMatch = msg.Text.match(/[?#&]token=([A-Za-z0-9_=-]+)/);
    if (!tokenMatch) {
      throw new Error('magic-link token not found');
    }
    const token = tokenMatch[1];

    const verifyResp = await page.request.get(
      `/api/portal/${slug}/auth/verify?token=${encodeURIComponent(token)}`,
      { headers: SEC_FETCH }
    );
    expect(verifyResp.ok(), `verify: ${verifyResp.status()}`).toBeTruthy();

    // 4. Land on the portal home with the customer session active.
    await page.goto(`${BASE_URL}/portal/${slug}`);

    // 5. Open the request form. The card is the first request-type button on
    //    the page (the section has exactly one request type wired into it).
    //    Wait for the auto-save POST to /drafts to know the modal has fired.
    const requestTypeCard = page.getByRole('button', { name: rt.name });
    await expect(requestTypeCard).toBeVisible({ timeout: 5000 });
    await requestTypeCard.click();

    // 6. Step 1: fill the title field, advance to step 2. The advance click
    //    triggers goToNextStep → saveDraft. Wait for the actual POST so the
    //    next read is guaranteed to see the draft row.
    await expect(page.locator('#request-title')).toBeVisible({ timeout: 5000 });
    await page.locator('#request-title').fill(draftTitle);

    const savePromise = page.waitForResponse(
      (resp) =>
        resp.url().includes(`/api/portal/${slug}/drafts`) &&
        resp.request().method() === 'POST' &&
        resp.ok()
    );
    await page.locator('[data-testid="request-form-next-step"]').click();
    await savePromise;

    // 7. Close the modal — clicking the X button or pressing Escape both
    //    leave the saved draft in place (no cleanup-on-close).
    await page.keyboard.press('Escape');
    await expect(page.locator('#request-title')).toBeHidden({ timeout: 5000 });

    // 8. Reopen the same request-type card. The modal hydrates the draft via
    //    GET /portal/{slug}/drafts/{requestTypeId}; resume banner should
    //    appear, title should be prefilled, and current_step should be 2
    //    (which means the description field — only on step 2 — is visible).
    await requestTypeCard.click();
    await expect(page.locator('[data-testid="request-form-draft-resume-banner"]')).toBeVisible({
      timeout: 5000,
    });

    // Per RequestFormModal: when current_step lands on 2, step-1's title
    // input is no longer in the DOM (only fields for the current step
    // render). So we assert the resume worked by checking the description
    // field on step 2 is visible AND going back to step 1 shows the title
    // prefilled.
    await expect(page.locator('#request-description')).toBeVisible({ timeout: 5000 });
    await page.locator('[data-testid="request-form-back-step"]').click();
    await expect(page.locator('#request-title')).toHaveValue(draftTitle, {
      timeout: 5000,
    });

    // 9. Cleanup so reruns on the same DB don't observe a stale draft.
    const deleteResp = await page.request.delete(`/api/portal/${slug}/drafts/${rt.id}`, {
      headers: SEC_FETCH,
    });
    expect([200, 204].includes(deleteResp.status())).toBeTruthy();
  });
});
