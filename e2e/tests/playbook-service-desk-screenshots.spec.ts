import { expect, test } from '../fixtures/mail';
import { mkdirSync } from 'node:fs';
import { resolve } from 'node:path';
import { authenticateAdminRequest, createWorkspaceViaAPI } from '../fixtures/api-helpers';

/**
 * Playbook screenshots for the "Run an internal service desk" page on
 * windshift.sh.
 *
 * Seeds an IT workspace with a portal, request types, an asset set, and a
 * support queue, then captures the real product surfaces. The passkey banner
 * is dismissed before the portal shot so the marketing image is clean. The
 * asset is linked to the portal ticket so the shots show the connection, not
 * just a list of devices.
 *
 * Writes PNGs to windshift-website/public/originals so the website's
 * optimize-images script produces the webp variants.
 *
 * Run from windshift-website:
 *   PLAYBOOK_SCREENSHOTS=1 ../../windshift-core/core-tests/run-e2e.sh \
 *     tests/playbook-service-desk-screenshots.spec.ts --workers=1
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

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

let workspaceId = 0;
let portalSlug = '';
let assetId = 0;
let requestItemId = 0;

async function api(
  request: any,
  method: 'get' | 'post' | 'put' | 'patch' | 'delete',
  path: string,
  data?: Record<string, unknown>
) {
  const resp = await request[method](`${BASE_URL}${path}`, {
    headers: SEC_FETCH,
    ...(data ? { data } : {}),
  });
  expect(resp.ok(), `${method} ${path}: ${resp.status()} ${await resp.text()}`).toBeTruthy();
  const text = await resp.text();
  if (!text) return null;
  const body = JSON.parse(text);
  return body.data ?? body;
}

async function tryApi(
  request: any,
  method: 'get' | 'post' | 'put' | 'patch' | 'delete',
  path: string,
  data?: Record<string, unknown>
) {
  const resp = await request[method](`${BASE_URL}${path}`, {
    headers: SEC_FETCH,
    ...(data ? { data } : {}),
  });
  if (!resp.ok()) return null;
  const text = await resp.text();
  if (!text) return null;
  const body = JSON.parse(text);
  return body.data ?? body;
}

// The support queue is opt-in (the helpdesk pack enables it). Enable it on the
// workspace nav through the board configuration so the queue view is reachable.
async function enableQueueView(request: any, wsId: number) {
  const config = await tryApi(request, 'get', `/api/v2/workspaces/${wsId}/board-configuration`);
  await api(request, 'put', `/api/v2/workspaces/${wsId}/board-configuration`, {
    columns: config?.columns || [],
    backlog_status_ids: config?.backlog_status_ids || [],
    list_columns: config?.list_columns || [],
    roadmap_config: config?.roadmap_config || null,
    card_fields: (config?.card_fields || []).map((f: any, i: number) => ({
      field_identifier: f.field_identifier,
      field_type: f.field_type,
      display_order: f.display_order ?? i,
      width: f.width ?? 0,
    })),
    show_rightmost_column_last_50: Boolean(config?.show_rightmost_column_last_50),
    completed_item_retention_days: config?.completed_item_retention_days ?? null,
    view_settings: {
      enabled_views: [
        'backlog',
        'board',
        'list',
        'tree',
        'map',
        'roadmap',
        'queue',
        'agents',
        'iterations',
        'milestones',
        'analytics',
        'actions',
        'pages',
      ],
    },
  });
}

async function createRequestType(
  request: any,
  channelId: number,
  wsId: number,
  name: string,
  description: string,
  icon: string,
  color: string
) {
  const resp = await request.post(`/api/channels/${channelId}/request-types`, {
    headers: SEC_FETCH,
    data: { name, description, icon, color, item_type_id: 1, is_active: true, workspace_id: wsId },
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

async function settle(page: any) {
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

test('capture the internal service desk portal', async ({ request, mail, page }) => {
  mail.skipIfMissing();
  await authenticateAdminRequest(request);
  mkdirSync(outputDir, { recursive: true });
  await page.setViewportSize({ width: 1440, height: 900 });

  const stamp = Date.now();
  const slug = `acme-it-${stamp}`;
  const employeeEmail = `emily.wang-${stamp}@windshift.test`;

  const workspace = await createWorkspaceViaAPI(request, {
    name: 'IT Infrastructure',
    key: 'IT',
    description: 'Internal IT service desk: devices, access, and employee requests.',
  });
  workspaceId = workspace.id;
  await enableQueueView(request, workspaceId);

  // Statuses for the queue tickets.
  const statuses = await api(request, 'get', `/api/v2/workspaces/${workspaceId}/statuses`);
  const statusByName = new Map(statuses.map((s: any) => [String(s.name).toLowerCase(), s]));
  const open = statusByName.get('open') || statuses[0];
  const inProgress = statusByName.get('in progress') || statuses[1] || open;

  const priorities = await api(request, 'get', `/api/v2/workspaces/${workspaceId}/priorities`);
  const priorityByName = new Map(
    priorities.map((p: any) => [String(p.name).toLowerCase(), p])
  );
  const high = priorityByName.get('high') || priorities[0];
  const medium = priorityByName.get('medium') || priorities[1] || high;
  const low = priorityByName.get('low') || priorities[2] || medium;

  // Portal channel.
  const channel = await api(request, 'post', '/api/channels', {
    name: 'ACME Corp IT Service Management',
    type: 'portal',
    direction: 'inbound',
    status: 'disabled',
    slug,
  });
  portalSlug = slug;
  await api(request, 'put', `/api/channels/${channel.id}/config`, {
    config: {
      portal_slug: slug,
      portal_workspace_ids: [workspaceId],
      portal_title: 'ACME Corp IT Service Management',
      // Windshift brand gradient (blue to teal) instead of the default purple.
      portal_gradient: 19,
      portal_registration_mode: 'open',
    },
  });
  await api(request, 'put', `/api/channels/${channel.id}/toggle`);

  const hardware = await createRequestType(
    request,
    channel.id,
    workspaceId,
    'Report a hardware issue',
    'A device will not start, charge, or connect.',
    'Server',
    '#2563eb'
  );
  const access = await createRequestType(
    request,
    channel.id,
    workspaceId,
    'Request access',
    'Get access to a system, folder, or shared drive.',
    'Key',
    '#7c3aed'
  );
  const network = await createRequestType(
    request,
    channel.id,
    workspaceId,
    'Report a network issue',
    'Report slow Wi-Fi, a dropped VPN, or an outage.',
    'Wifi',
    '#0891b2'
  );
  const software = await createRequestType(
    request,
    channel.id,
    workspaceId,
    'Request software',
    'Request a licence or install an approved application.',
    'Package',
    '#16a34a'
  );
  const password = await createRequestType(
    request,
    channel.id,
    workspaceId,
    'Reset my password',
    'Reset a password or unlock an account.',
    'Lock',
    '#dc2626'
  );
  const room = await createRequestType(
    request,
    channel.id,
    workspaceId,
    'Book a meeting room',
    'Book a meeting room or a shared desk.',
    'Calendar',
    '#d97706'
  );

  // One titled section so the hub reads like a real service catalogue.
  await api(request, 'put', `/api/channels/${channel.id}/config`, {
    config: {
      portal_sections: [
        {
          id: `e2e-section-${channel.id}`,
          title: 'How can IT help?',
          subtitle: 'Pick a request type. The form asks for the details the team needs to act.',
          display_order: 0,
          request_type_ids: [hardware.id, access.id, network.id, software.id, password.id, room.id],
          asset_report_ids: [],
        },
      ],
    },
  });

  // Asset set with types, categories, statuses, and assets.
  const set = await api(request, 'post', '/api/v2/asset-sets', { name: 'IT Infrastructure' });
  const laptopType = await api(request, 'post', `/api/v2/asset-sets/${set.id}/types`, { name: 'Laptop' });
  const phoneType = await api(request, 'post', `/api/v2/asset-sets/${set.id}/types`, { name: 'Phone' });
  const licenseType = await api(request, 'post', `/api/v2/asset-sets/${set.id}/types`, { name: 'License' });
  const engineering = await api(request, 'post', `/api/v2/asset-sets/${set.id}/categories`, { name: 'Engineering' });
  const sales = await api(request, 'post', `/api/v2/asset-sets/${set.id}/categories`, { name: 'Sales' });
  const support = await api(request, 'post', `/api/v2/asset-sets/${set.id}/categories`, { name: 'Support' });
  // Sets ship with default statuses (Active, Inactive, Maintenance, Retired).
  const assetStatuses = await api(request, 'get', `/api/v2/asset-sets/${set.id}/statuses`);
  const assetStatusByName = new Map(
    assetStatuses.map((s: any) => [String(s.name).toLowerCase(), s])
  );
  const active = assetStatusByName.get('active') || assetStatuses[0];
  const repair = assetStatusByName.get('maintenance') || active;
  const retired = assetStatusByName.get('retired') || active;

  const laptopRows: Array<[string, any, any, string]> = [
    ['MacBook Pro 14" M3 - Emily Wang', engineering, repair, 'IT-1042'],
    ['MacBook Pro 14" M3 - Alex Kim', engineering, active, 'IT-1043'],
    ['MacBook Pro 16" M3 Max - David Lee', engineering, active, 'IT-1044'],
    ['ThinkPad X1 Carbon - Tom Brown', sales, active, 'IT-1045'],
    ['Dell XPS 13 - Maria Garcia', support, active, 'IT-1046'],
    ['MacBook Air M2 - Sarah Chen', sales, retired, 'IT-1047'],
  ];
  for (const [title, category, status, tag] of laptopRows) {
    const a = await api(request, 'post', `/api/v2/asset-sets/${set.id}/assets`, {
      title,
      asset_type_id: laptopType.id,
      category_id: category.id,
      status_id: status.id,
      asset_tag: tag,
    });
    if (!assetId) assetId = a.id;
  }
  await api(request, 'post', `/api/v2/asset-sets/${set.id}/assets`, {
    title: 'iPhone 15 - Sales pool',
    asset_type_id: phoneType.id,
    category_id: sales.id,
    status_id: active.id,
    asset_tag: 'IT-2044',
  });
  await api(request, 'post', `/api/v2/asset-sets/${set.id}/assets`, {
    title: 'Figma Organization',
    asset_type_id: licenseType.id,
    category_id: engineering.id,
    status_id: active.id,
    asset_tag: 'LIC-0007',
  });

  // Additional tickets so the support queue is not a single row.
  const queueTickets: Array<[string, any, any]> = [
    ['VPN disconnects every few minutes', open, high],
    ['New starter needs access to the finance drive', open, medium],
    ['Meeting room display shows no signal', open, low],
    ['Phone will not sync company email', inProgress, high],
    ['Replace the desk monitor in Zurich', open, medium],
  ];
  for (const [title, status, priority] of queueTickets) {
    await api(request, 'post', '/api/v2/items', {
      workspace_id: workspaceId,
      title,
      status_id: status.id,
      priority_id: priority?.id,
    });
  }

  // Customer login via magic link.
  await page.context().clearCookies();
  await page.goto(`${BASE_URL}/portal/${slug}`);
  await page.locator('#portal-sign-in').click();
  const emailInput = page.locator('#email');
  await expect(emailInput).toBeVisible({ timeout: 5000 });
  const since = new Date();
  await emailInput.fill(employeeEmail);
  await page.getByTestId('portal-login-request-magic-link').click();

  const msg = await mail.waitForLast({
    to: employeeEmail,
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

  // Dismiss the passkey banner so the marketing shot is clean.
  const passkeyBanner = page.getByTestId('portal-passkey-banner');
  if (await passkeyBanner.isVisible().catch(() => false)) {
    await passkeyBanner.locator('button').last().click();
    await expect(passkeyBanner).toHaveCount(0, { timeout: 5000 });
  }
  await settle(page);

  // 1. Portal hub with the internal request types.
  await page.screenshot({
    path: `${outputDir}/service-desk-portal.png`,
    fullPage: false,
    animations: 'disabled',
  });

  // 2. Hardware request form, filled in.
  await page.getByTestId('portal-request-type-card').filter({ hasText: hardware.name }).click();
  await expect(page.locator('#request-title')).toBeVisible({ timeout: 5000 });
  await page.locator('#request-title').fill('Laptop will not charge after the latest update');
  await page
    .locator('#request-description')
    .fill(
      'The laptop works on the dock but does not charge from the USB-C charger. The battery stays at 12 percent. This is the second time this week.'
    );
  await settle(page);
  await page.screenshot({
    path: `${outputDir}/service-desk-request-form.png`,
    fullPage: false,
    animations: 'disabled',
  });

  // 3. Submit and capture the employee's request detail.
  const submitPromise = page.waitForResponse(
    (resp) => resp.url().includes(`/api/portal/${slug}/submit`) && resp.request().method() === 'POST'
  );
  await page.getByTestId('request-form-submit').click();
  const submitResp = await submitPromise;
  expect(submitResp.ok(), `submit: ${submitResp.status()}`).toBeTruthy();
  const submitBody = await submitResp.json();
  requestItemId = submitBody.id ?? submitBody.item_id ?? submitBody.item?.id;
  expect(requestItemId, 'submit response missing item id').toBeGreaterThan(0);
  await expect(page).toHaveURL(new RegExp(`[?&]view=requests(?:&id=${requestItemId})?`), {
    timeout: 10000,
  });
  await expect(page.getByTestId('portal-request-detail-title')).toBeVisible({ timeout: 5000 });

  // Seed the thread: an internal note for the team and a public update the
  // employee sees on the request.
  await api(request, 'post', `/api/v2/items/${requestItemId}/comments`, {
    content:
      'Known charger issue after the firmware update. Replacement approved and a loaner is reserved at the IT desk.',
    is_private: true,
  });
  await api(request, 'post', `/api/v2/items/${requestItemId}/comments`, {
    content:
      'We reserved a replacement charger and a loaner laptop. Collect both from the IT desk today.',
  });

  // Link the laptop to the ticket so the asset record shows the request.
  const linkTypes = await api(request, 'get', '/api/v2/link-types');
  const relates = linkTypes.find((t: any) => /relate/i.test(t.name)) || linkTypes[0];
  await api(request, 'post', `/api/v2/assets/${assetId}/links`, {
    link_type_id: relates.id,
    target_type: 'item',
    target_id: requestItemId,
  });

  await page.reload();
  await expect(page.getByText('Collect both from the IT desk today.').first()).toBeVisible({
    timeout: 10000,
  });
  await settle(page);
  await page.screenshot({
    path: `${outputDir}/service-desk-request-detail.png`,
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

  // Sidebar icon buttons: edit mode, hero gradient, navigation, request types.
  await page.getByTestId('portal-customize-panel').locator('button').nth(3).click();
  await expect(page.getByText('Request Types').first()).toBeVisible({ timeout: 5000 });
  await page.getByRole('button', { name: /Fields \(/ }).first().click();
  await expect(page.getByTestId('portal-fields-builder')).toBeVisible({ timeout: 5000 });
  await settle(page);

  // Crop to the customize panel plus the fields builder.
  await page.screenshot({
    path: `${outputDir}/service-desk-request-type-builder.png`,
    clip: { x: 0, y: 0, width: 928, height: 700 },
    animations: 'disabled',
  });
});

test('capture the support queue', async ({ request, page }) => {
  test.skip(!workspaceId, 'No workspace captured.');
  await authenticateAdminRequest(request);
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`/workspaces/${workspaceId}/queue`);
  await expect(page.getByTestId('support-queue-view')).toBeVisible({ timeout: 10000 });
  await expect(page.getByTestId('support-queue-table')).toBeVisible({ timeout: 10000 });
  await settle(page);
  await page.screenshot({
    path: `${outputDir}/service-desk-queue.png`,
    fullPage: false,
    animations: 'disabled',
  });
});

test('capture the asset and the linked ticket', async ({ request, page }) => {
  test.skip(!assetId || !requestItemId, 'No asset or ticket captured.');
  await authenticateAdminRequest(request);
  await page.setViewportSize({ width: 1440, height: 900 });

  // Asset detail with the linked item count.
  await page.goto(`/assets/${assetId}`);
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible({ timeout: 10000 });
  await expect(page.getByText('Linked Items')).toBeVisible({ timeout: 10000 });
  await settle(page);
  await page.screenshot({
    path: `${outputDir}/service-desk-asset.png`,
    fullPage: false,
    animations: 'disabled',
  });

  // Relationship graph: the asset and its linked tickets in one view.
  const graphButton = page.locator('button[title="Relationship Graph"]').first();
  if (await graphButton.isVisible().catch(() => false)) {
    await graphButton.click();
    await expect(page.locator('.svelte-flow__node').first()).toBeVisible({ timeout: 10000 });
    await settle(page);
    await page.screenshot({
      path: `${outputDir}/service-desk-asset-graph.png`,
      fullPage: false,
      animations: 'disabled',
    });
    await page.keyboard.press('Escape');
  }

  // Ticket with the customer context and the linked asset.
  await page.goto(`/workspaces/${workspaceId}/items/${requestItemId}`);
  await expect(page.getByTestId('customer-context-toggle')).toBeVisible({ timeout: 10000 });
  await page.getByTestId('customer-context-toggle').click();
  await expect(page.getByTestId('customer-context-assets')).toBeVisible({ timeout: 10000 });
  await expect(page.getByTestId(`customer-context-asset-${assetId}`)).toBeVisible({
    timeout: 10000,
  });
  await settle(page);
  await page.screenshot({
    path: `${outputDir}/service-desk-ticket-asset.png`,
    fullPage: false,
    animations: 'disabled',
  });
});
