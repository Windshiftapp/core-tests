import { expect, test } from '../fixtures/mail';
import { mkdirSync } from 'node:fs';
import { resolve } from 'node:path';
import { authenticateAdminRequest, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { attachRequestTypesToSection } from '../helpers/portal-setup';

/**
 * Playbook screenshots for the "Customer bug reports that feed your backlog"
 * page on windshift.sh.
 *
 * Drives the real customer portal (magic-link login via Mailpit) and the real
 * workspace, then writes PNGs to windshift-website/public/originals so the
 * website's optimize-images script produces the webp variants.
 *
 * Run from windshift-website:
 *   PLAYBOOK_SCREENSHOTS=1 ../../windshift-core/core-tests/run-e2e.sh \
 *     tests/playbook-screenshots.spec.ts --workers=1
 */

const enabled = process.env.PLAYBOOK_SCREENSHOTS === '1';
const outputDir = resolve(
  process.env.PLAYBOOK_SCREENSHOTS_DIR || '../../../windshift-website/public/originals'
);
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const BASE_ORIGIN = process.env.BASE_ORIGIN || new URL(BASE_URL).origin;

test.skip(!enabled, 'Set PLAYBOOK_SCREENSHOTS=1 to generate playbook screenshots.');
test.describe.configure({ mode: 'serial', retries: 0 });
// Capture at 2x so the website can serve crisp hero@2x variants on retina.
test.use({ deviceScaleFactor: 2 });

let workspaceId = 0;
let itemId = 0;
let portalSlug = '';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

// The shared portal-setup helper predates the workspace_id requirement on
// request-type creation, so the screenshot spec creates its own.
async function createRequestType(request, channelId, workspaceId, name) {
  const resp = await request.post(`/api/channels/${channelId}/request-types`, {
    headers: SEC_FETCH,
    data: { name, item_type_id: 1, is_active: true, workspace_id: workspaceId },
  });
  expect(resp.ok(), `create request type: ${resp.status()} ${await resp.text()}`).toBeTruthy();
  const rt = await resp.json();
  const fieldsResp = await request.put(`/api/channels/${channelId}/request-types/${rt.id}/fields`, {
    headers: SEC_FETCH,
    data: [
      { field_identifier: 'title', field_type: 'default', display_order: 0, is_required: true, step_number: 1 },
      { field_identifier: 'description', field_type: 'default', display_order: 1, is_required: false, step_number: 1 },
    ],
  });
  expect(
    fieldsResp.ok(),
    `set request type fields: ${fieldsResp.status()} ${await fieldsResp.text()}`
  ).toBeTruthy();
  return { id: rt.id, name: rt.name };
}

async function settle(page) {
  await page
    .addStyleTag({
      content: `
        *, *::before, *::after {
          animation-duration: 0s !important;
          animation-delay: 0s !important;
          transition-duration: 0s !important;
          transition-delay: 0s !important;
        }
      `,
    })
    .catch(() => {});
  await page.waitForLoadState('networkidle', { timeout: 5000 }).catch(() => {});
}

test('capture the customer portal flow', async ({ request, mail, page }) => {
  mail.skipIfMissing();
  await authenticateAdminRequest(request);
  mkdirSync(outputDir, { recursive: true });
  await page.setViewportSize({ width: 1440, height: 900 });

  // Rename the seeded admin so the comment author reads like a person in the
  // screenshots instead of the E2E fixture name.
  const meResp = await request.get('/api/auth/me');
  const me = await meResp.json();
  const currentUser = me?.user ?? me;
  if (currentUser?.id) {
    const patchResp = await request.patch(`/api/v2/admin/users/${currentUser.id}`, {
      headers: { 'Content-Type': 'application/merge-patch+json', ...SEC_FETCH },
      data: {
        email: currentUser.email,
        username: currentUser.username,
        first_name: 'Alex',
        last_name: 'Rivera',
        timezone: currentUser.timezone || 'UTC',
        language: currentUser.language || 'en',
        is_active: currentUser.is_active ?? true,
      },
    });
    if (!patchResp.ok()) {
      console.log(`[playbook-screenshots] rename failed: ${patchResp.status()} ${await patchResp.text()}`);
    }
  }

  const stamp = Date.now();
  const slug = `acme-support-${stamp}`;
  const customerEmail = `maya.jensen-${stamp}@windshift.test`;

  // Create the workspace with a presentable name and key so the screenshots do
  // not carry the E2E test workspace naming.
  const workspace = await createWorkspaceViaAPI(request, {
    name: 'Commerce Platform',
    key: 'COMM',
    description: 'Customer requests, delivery work, and release readiness.',
  });
  workspaceId = workspace.id;

  const channelResp = await request.post('/api/channels', {
    headers: SEC_FETCH,
    data: {
      name: 'Acme Support',
      type: 'portal',
      direction: 'inbound',
      status: 'disabled',
      slug,
    },
  });
  expect(
    channelResp.ok(),
    `create portal channel: ${channelResp.status()} ${await channelResp.text()}`
  ).toBeTruthy();
  const channel = await channelResp.json();
  portalSlug = slug;

  const configResp = await request.put(`/api/channels/${channel.id}/config`, {
    headers: SEC_FETCH,
    data: {
      config: {
        portal_slug: slug,
        portal_workspace_ids: [workspaceId],
        portal_title: 'Acme Support',
        portal_registration_mode: 'open',
      },
    },
  });
  expect(
    configResp.ok(),
    `configure portal channel: ${configResp.status()} ${await configResp.text()}`
  ).toBeTruthy();
  const toggleResp = await request.put(`/api/channels/${channel.id}/toggle`, { headers: SEC_FETCH });
  expect(toggleResp.ok(), `enable portal channel: ${toggleResp.status()}`).toBeTruthy();

  const incident = await createRequestType(
    request,
    channel.id,
    workspaceId,
    'Report a production issue'
  );
  const access = await createRequestType(request, channel.id, workspaceId, 'Request access');
  const question = await createRequestType(
    request,
    channel.id,
    workspaceId,
    'Ask a product question'
  );
  await attachRequestTypesToSection(
    request,
    { channelId: channel.id, workspaceId, slug, name: 'Acme Support' },
    [incident.id, access.id, question.id]
  );

  // Customer login via magic link.
  await page.context().clearCookies();
  await page.goto(`${BASE_URL}/portal/${slug}`);
  await page.locator('#portal-sign-in').click();
  const emailInput = page.locator('#email');
  await expect(emailInput).toBeVisible({ timeout: 5000 });
  const since = new Date();
  await emailInput.fill(customerEmail);
  await page.getByTestId('portal-login-request-magic-link').click();

  const msg = await mail.waitForLast({
    to: customerEmail,
    subject: 'Sign in to your portal',
    since,
    timeoutMs: 5000,
  });
  const linkMatch = msg.Text.match(/(https?:\/\/\S+\/portal\/\S+\/verify#token=[^\s>]+)/);
  if (!linkMatch) throw new Error(`magic-link URL missing: ${msg.Text.slice(0, 200)}`);
  const link = linkMatch[1].replace(/^https?:\/\/[^/]+/, BASE_ORIGIN);
  await page.goto(link);
  await expect(page).toHaveURL(new RegExp(`/portal/${slug}/?$`), { timeout: 10000 });
  await expect(page.getByTestId('portal-page')).toHaveAttribute('data-ready', 'true');
  await expect(page.getByTestId('portal-request-type-card').first()).toBeVisible({
    timeout: 10000,
  });
  await settle(page);

  // 1. Portal hub with the request types.
  await page.screenshot({
    path: `${outputDir}/portal-hub.png`,
    fullPage: false,
    animations: 'disabled',
  });

  // 2. The request form, filled in.
  await page.getByTestId('portal-request-type-card').filter({ hasText: incident.name }).click();
  await expect(page.locator('#request-title')).toBeVisible({ timeout: 5000 });
  await page
    .locator('#request-title')
    .fill('Checkout API returns 502 after deployment');
  await page
    .locator('#request-description')
    .fill(
      'Errors started after release 2026.07.16. EU customers cannot complete checkout. Rollback is in progress.'
    );
  await settle(page);
  await page.screenshot({
    path: `${outputDir}/portal-request-form.png`,
    fullPage: false,
    animations: 'disabled',
  });

  // 3. Submit and capture the customer's request detail.
  const submitPromise = page.waitForResponse(
    (resp) =>
      resp.url().includes(`/api/portal/${slug}/submit`) && resp.request().method() === 'POST'
  );
  await page.getByTestId('request-form-submit').click();
  const submitResp = await submitPromise;
  expect(submitResp.ok(), `submit: ${submitResp.status()}`).toBeTruthy();
  const submitBody = await submitResp.json();
  itemId = submitBody.id ?? submitBody.item_id ?? submitBody.item?.id;
  expect(itemId, 'submit response missing item id').toBeGreaterThan(0);
  await expect(page).toHaveURL(new RegExp(`[?&]view=requests(?:&id=${itemId})?`), {
    timeout: 10000,
  });
  await expect(page.getByTestId('portal-request-detail-title')).toBeVisible({ timeout: 5000 });

  // Seed the thread before capturing: an internal note for the workspace and a
  // public update the customer sees on their request.
  await request.post(`/api/v2/items/${itemId}/comments`, {
    data: {
      content:
        'Release 2026.07.16 failed health checks. Rollback approved; keep the customer updated when traffic recovers.',
      is_private: true,
    },
  });
  await request.post(`/api/v2/items/${itemId}/comments`, {
    data: {
      content:
        'We identified the failed deployment and are rolling back the affected service. Checkout traffic is recovering.',
    },
  });

  await page.reload();
  await expect(
    page.getByText('Checkout traffic is recovering.').first()
  ).toBeVisible({ timeout: 10000 });
  await settle(page);
  await page.screenshot({
    path: `${outputDir}/portal-request-detail.png`,
    fullPage: false,
    animations: 'disabled',
  });
});

test('capture the internal work item', async ({ request, page }) => {
  test.skip(!itemId, 'No portal request captured.');
  await authenticateAdminRequest(request);
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`/workspaces/${workspaceId}/items/${itemId}`);
  await expect(page.locator('body')).toBeVisible();
  await settle(page);
  await page.screenshot({
    path: `${outputDir}/portal-work-item.png`,
    fullPage: false,
    animations: 'disabled',
  });
});

test('capture the request type builder', async ({ request, page }) => {
  test.skip(!portalSlug, 'No portal captured.');
  await authenticateAdminRequest(request);
  await page.setViewportSize({ width: 1440, height: 900 });

  await page.goto(`${BASE_URL}/portal/${portalSlug}`);
  await settle(page);
  await page.getByTestId('portal-settings-button').click();
  await page.getByTestId('portal-customize-button').click();
  await expect(page.getByTestId('portal-customize-panel')).toBeVisible({ timeout: 5000 });

  // The sidebar icon buttons are: edit mode, hero gradient, navigation,
  // request types. The request types section has no test id of its own.
  await page.getByTestId('portal-customize-panel').locator('button').nth(3).click();
  await expect(page.getByText('Request Types').first()).toBeVisible({ timeout: 5000 });

  // Open the inline fields builder for the first request type so the shot
  // shows the editing form, not just the list.
  await page.getByRole('button', { name: /Fields \(/ }).first().click();
  await expect(page.getByTestId('portal-fields-builder')).toBeVisible({ timeout: 5000 });
  await settle(page);

  // Crop to the customize panel (28rem) plus the fields builder (30rem) so the
  // portal behind the panels does not wash out the shot.
  await page.screenshot({
    path: `${outputDir}/portal-request-type-builder.png`,
    clip: { x: 0, y: 0, width: 928, height: 700 },
    animations: 'disabled',
  });
});
