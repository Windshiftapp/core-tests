import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/mail';
import { generateWorkspace } from '../fixtures/test-data';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const BASE_ORIGIN = process.env.BASE_ORIGIN || new URL(BASE_URL).origin;

test('anonymous bootstrap hides reports and a portal customer receives the safe report DTO', async ({
  request,
  mail,
  page,
}) => {
  mail.skipIfMissing();

  const stamp = Date.now();
  const slug = `e2e-safe-report-${stamp}`;
  const customerEmail = `${slug}@windshift.test`;
  const workspace = await createWorkspaceViaAPI(request, generateWorkspace(`safe-report-${stamp}`));

  const itemTypesResponse = await request.get('/api/item-types', {
    headers: SEC_FETCH,
  });
  expect(itemTypesResponse.ok()).toBeTruthy();
  const itemTypes = await itemTypesResponse.json();
  const itemTypeID = itemTypes[0]?.id;
  expect(itemTypeID).toBeGreaterThan(0);

  const channelResponse = await request.post('/api/channels', {
    headers: SEC_FETCH,
    data: {
      name: `Safe report portal ${stamp}`,
      type: 'portal',
      direction: 'inbound',
      status: 'disabled',
      slug,
    },
  });
  expect(channelResponse.ok(), `create portal: ${await channelResponse.text()}`).toBeTruthy();
  const channel = await channelResponse.json();
  const portalConfig = {
    portal_slug: slug,
    portal_workspace_ids: [workspace.id],
    portal_title: 'Safe report portal',
    portal_registration_mode: 'open',
    portal_sections: [],
  };
  const initialConfigResponse = await request.put(`/api/channels/${channel.id}/config`, {
    headers: SEC_FETCH,
    data: { config: portalConfig },
  });
  expect(
    initialConfigResponse.ok(),
    `configure portal workspace: ${await initialConfigResponse.text()}`
  ).toBeTruthy();

  const assetSetResponse = await request.post('/api/asset-sets', {
    headers: SEC_FETCH,
    data: { name: `Safe report assets ${stamp}` },
  });
  expect(assetSetResponse.ok(), `create asset set: ${await assetSetResponse.text()}`).toBeTruthy();
  const assetSet = await assetSetResponse.json();

  const reportResponse = await request.post(`/api/channels/${channel.id}/asset-reports`, {
    headers: SEC_FETCH,
    data: {
      name: `Guest form report ${stamp}`,
      description: 'Rendered from safe metadata',
      asset_set_id: assetSet.id,
      cql_query: `title ~ "internal-${stamp}"`,
      icon: 'Table2',
      color: '#123456',
      column_config: ['title'],
      run_mode: 'form',
      item_type_id: itemTypeID,
      workspace_id: workspace.id,
      config: JSON.stringify({
        require_auth: true,
        success_message: 'Guest report complete',
        submit_button_text: 'Run guest report',
        redirect_url: 'https://internal.example.test/secret',
      }),
    },
  });
  expect(reportResponse.ok(), `create report: ${await reportResponse.text()}`).toBeTruthy();
  const report = await reportResponse.json();

  const configResponse = await request.put(`/api/channels/${channel.id}/config`, {
    headers: SEC_FETCH,
    data: {
      config: {
        ...portalConfig,
        portal_sections: [
          {
            id: 'guest-reports',
            title: 'Guest reports',
            subtitle: '',
            display_order: 0,
            request_type_ids: [],
            asset_report_ids: [report.id],
          },
        ],
      },
    },
  });
  expect(configResponse.ok(), `configure portal: ${await configResponse.text()}`).toBeTruthy();
  const toggleResponse = await request.put(`/api/channels/${channel.id}/toggle`, {
    headers: SEC_FETCH,
  });
  expect(toggleResponse.ok(), `enable portal: ${await toggleResponse.text()}`).toBeTruthy();

  await page.context().clearCookies();
  const bootstrapPromise = page.waitForResponse((response) =>
    response.url().endsWith(`/api/portal/${slug}/bootstrap`)
  );
  await page.goto(`/portal/${slug}`);
  const bootstrapResponse = await bootstrapPromise;
  const bootstrap = await bootstrapResponse.json();
  expect(bootstrap.asset_reports).toEqual([]);
  expect(bootstrap.request_types).toEqual([]);
  expect(bootstrap.portal).not.toHaveProperty('channel_id');
  expect(bootstrap.portal).not.toHaveProperty('workspace_ids');
  expect(bootstrap.portal).not.toHaveProperty('sections');
  await expect(page.locator(`#portal-asset-form-${report.id}`)).toBeHidden();

  await page.locator('#portal-sign-in').click();
  await expect(page.locator('#email')).toBeVisible();
  const since = new Date(Date.now() - 2_000);
  await page.locator('#email').fill(customerEmail);
  await page.getByTestId('portal-login-request-magic-link').click();
  const message = await mail.waitForLast({
    to: customerEmail,
    subject: 'Sign in to your portal',
    since,
    timeoutMs: 5_000,
  });
  const link = message.Text.match(/(https?:\/\/\S+\/portal\/\S+\/verify#token=[^\s>]+)/)?.[1];
  expect(link, 'magic-link URL missing').toBeTruthy();
  if (!link) throw new Error('magic-link URL missing');
  await page.goto(link.replace(/^https?:\/\/[^/]+/, BASE_ORIGIN));

  await expect(page.locator(`#portal-asset-form-${report.id}`)).toBeVisible();
  const authenticatedBootstrapResponse = await page.request.get(`/api/portal/${slug}/bootstrap`);
  expect(authenticatedBootstrapResponse.ok()).toBeTruthy();
  const authenticatedBootstrap = await authenticatedBootstrapResponse.json();
  const publicReport = authenticatedBootstrap.asset_reports.find(
    (candidate: { id: number }) => candidate.id === report.id
  );
  expect(Object.keys(publicReport).sort()).toEqual(
    [
      'color',
      'column_config',
      'config',
      'description',
      'display_order',
      'icon',
      'id',
      'item_type_id',
      'name',
      'run_mode',
      'workspace_id',
    ].sort()
  );
  expect(publicReport.config).toEqual({
    success_message: 'Guest report complete',
    submit_button_text: 'Run guest report',
  });

  await expect(page.locator('#portal-login-title')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.locator('#portal-login-title')).toBeHidden();
  await page.locator(`#portal-asset-form-${report.id}`).click();
  await expect(page.locator('#portal-asset-report-form')).toBeVisible();
  await expect(page.getByTestId('portal-asset-report-form-submit')).toContainText(
    'Run guest report'
  );
});
