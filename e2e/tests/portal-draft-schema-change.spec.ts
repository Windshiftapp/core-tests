import type { APIRequestContext } from '../fixtures/context-path';
import { expect, test } from '../fixtures/mail';
import {
  attachRequestTypesToSection,
  createPortalChannel,
  createTwoStepRequestType,
} from '../helpers/portal-setup';

/**
 * Portal draft survives a mid-flow schema change.
 *
 * Scenario (test plan §4):
 *   1. Customer signs in via magic link and starts a request, filling step 1
 *      and advancing — auto-save persists a draft pinned at step 2.
 *   2. Admin mutates the request-type schema while the draft is still open:
 *      flattens the form back into a single step and removes the optional
 *      description field.
 *   3. Customer resumes the form. The draft must hydrate without crashing,
 *      the surviving fields must be prefilled from the draft, and a final
 *      submit must succeed and produce an item with the customer's title.
 *
 * Risk covered:
 *   - The hydration path: a draft pointing at a step that no longer exists.
 *   - Field cleanup: stale field values in the draft that are no longer in
 *     the schema.
 *   - End-to-end validity: the submitted item must reflect *current* schema,
 *     not stale draft schema, and must succeed even though the schema moved.
 *
 * Skips when Mailpit is unavailable.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

interface FieldUpdate {
  field_identifier: string;
  field_type: 'default' | 'custom' | 'virtual';
  display_order: number;
  is_required: boolean;
  step_number: number;
}

async function setRequestTypeFields(
  request: APIRequestContext,
  channelId: number,
  requestTypeId: number,
  fields: FieldUpdate[]
): Promise<void> {
  const resp = await request.put(
    `/api/channels/${channelId}/request-types/${requestTypeId}/fields`,
    { headers: SEC_FETCH, data: fields }
  );
  expect(
    resp.ok(),
    `update request-type fields: ${resp.status()} ${await resp.text()}`
  ).toBeTruthy();
}

test.describe('Portal draft + magic link + schema change', () => {
  test('draft survives a flatten + field-removal between save and resume; final submit lands an item', async ({
    request,
    mail,
    page,
  }) => {
    mail.skipIfMissing();
    test.setTimeout(90_000);

    const stamp = Date.now();
    const slug = `e2e-dsc-${stamp}`;
    const customerEmail = `e2e-dsc-${stamp}@windshift.test`;
    const draftTitle = `schema-change ${stamp}`;

    // 1. Setup: portal channel + 2-step request type (title @ step 1,
    //    description @ step 2). Wire into a section so the card shows up.
    const channel = await createPortalChannel(request, {
      slug,
      name: `Schema-Change ${stamp}`,
    });
    const rt = await createTwoStepRequestType(request, channel.channelId, {
      name: 'Two-step request',
    });
    await attachRequestTypesToSection(request, channel, [rt.id]);

    // 2. Drop admin cookies, sign the customer in via magic link. Same
    //    pattern as portal-draft-resume.spec.ts — API path is fine here,
    //    the UI login flow is already covered by portal-happy-path.
    await page.context().clearCookies();
    const since = new Date();
    const linkResp = await page.request.post(`/api/portal/${slug}/auth/request`, {
      headers: SEC_FETCH,
      data: { email: customerEmail },
    });
    expect(linkResp.ok(), `magic-link request: ${linkResp.status()}`).toBeTruthy();

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
    const verifyResp = await page.request.get(
      `/api/portal/${slug}/auth/verify?token=${encodeURIComponent(tokenMatch[1])}`,
      { headers: SEC_FETCH }
    );
    expect(verifyResp.ok(), `verify: ${verifyResp.status()}`).toBeTruthy();

    // 3. Open the form, fill step 1, advance to step 2 — saveDraft fires on
    //    step-advance and persists `current_step=2`. That's the stale value
    //    we want when admin flattens the form to a single step below.
    await page.goto(`${BASE_URL}/portal/${slug}`);
    const card = page.getByRole('button', { name: rt.name });
    await expect(card).toBeVisible({ timeout: 10_000 });
    await card.click();

    await expect(page.locator('#request-title')).toBeVisible({ timeout: 5000 });
    await page.locator('#request-title').fill(draftTitle);

    const advanceSave = page.waitForResponse(
      (resp) =>
        resp.url().includes(`/api/portal/${slug}/drafts`) &&
        resp.request().method() === 'POST' &&
        resp.ok(),
      { timeout: 15_000 }
    );
    await page.locator('[data-testid="request-form-next-step"]').click();
    await advanceSave;

    // On step 2 now — confirm the description field rendered. We don't fill
    // it: the schema mutation below will remove the field entirely, and we
    // assert the resumed form ignores stale field values regardless of
    // whether they were ever persisted.
    await expect(page.locator('#request-description')).toBeVisible({
      timeout: 5000,
    });

    // Close the modal, leaving the draft in place.
    await page.keyboard.press('Escape');
    await expect(page.locator('#request-description')).toBeHidden({
      timeout: 5000,
    });

    // 4. Admin: flatten the form. Title stays on step 1 (still required);
    //    description is removed entirely. This is the most aggressive shape
    //    change the FE has to swallow — both "current_step is now invalid"
    //    AND "a draft field has no schema home" at once.
    await setRequestTypeFields(request, channel.channelId, rt.id, [
      {
        field_identifier: 'title',
        field_type: 'default',
        display_order: 0,
        is_required: true,
        step_number: 1,
      },
    ]);

    // 5. Reopen the form. The draft hydrates against the new schema.
    await card.click();

    // Resume banner appears because a draft row still exists for this
    // (channel, request_type, customer) tuple, even after the schema change.
    await expect(page.locator('[data-testid="request-form-draft-resume-banner"]')).toBeVisible({
      timeout: 5000,
    });

    // The surviving field carries the draft value (title from step 1).
    await expect(page.locator('#request-title')).toBeVisible({ timeout: 5000 });
    await expect(page.locator('#request-title')).toHaveValue(draftTitle, {
      timeout: 5000,
    });

    // The removed field is *not* visible — its input shouldn't be in the DOM.
    await expect(page.locator('#request-description')).toHaveCount(0);

    // 6. Submit. The form now has one step + one required field, both
    //    already satisfied. Backend should accept and return an item id.
    const submitPromise = page.waitForResponse(
      (resp) =>
        resp.url().includes(`/api/portal/${slug}/submit`) && resp.request().method() === 'POST'
    );
    await page.locator('[data-testid="request-form-submit"]').click();
    const submitResp = await submitPromise;
    expect(
      submitResp.ok(),
      `submit: ${submitResp.status()} ${await submitResp.text()}`
    ).toBeTruthy();
    const submitBody = await submitResp.json();
    const itemId: number = submitBody.id ?? submitBody.item_id ?? submitBody.item?.id;
    expect(itemId, 'submit response missing item id').toBeGreaterThan(0);

    // 7. Final invariant — admin sees the new item carrying our title.
    const itemResp = await request.get(`/api/items/${itemId}`, {
      headers: SEC_FETCH,
    });
    expect(itemResp.ok(), `get item ${itemId}: ${itemResp.status()}`).toBeTruthy();
    const item = await itemResp.json();
    expect(item.title).toBe(draftTitle);
  });
});
