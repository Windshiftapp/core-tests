import { authenticateAdminRequest } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { attachRequestTypesToSection, createPortalChannel, createSimpleRequestType } from '../helpers/portal-setup';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

/**
 * Portal request-type routes mirror KB article routes. This spec covers the
 * URL/prefill contract and the asset-report row action that builds the link.
 */
test('opens a prefilled request form from a direct route URL', async ({ page, request }) => {
  await authenticateAdminRequest(request);

  const stamp = Date.now();
  const slug = `e2e-request-prefill-${stamp}`;
  const channel = await createPortalChannel(request, { slug, name: `Prefill ${stamp}` });
  const rt = await createSimpleRequestType(request, channel, {
    name: `Prefill form ${stamp}`,
  });
  await attachRequestTypesToSection(request, channel, [rt.id]);

  await page.goto(
    `/portal/${slug}/request/${rt.id}?prefill.title=${encodeURIComponent('Prefilled title')}&prefill.description=${encodeURIComponent('Prefilled details')}`
  );
  await expect(page.getByTestId('portal-page')).toHaveAttribute('data-ready', 'true');

  await expect(page.locator('#request-title')).toHaveValue('Prefilled title');
  await expect(page.locator('#request-description')).toHaveValue('Prefilled details');

  const submitPromise = page.waitForResponse(
    (response) =>
      response.url().includes(`/api/portal/${slug}/submit`) &&
      response.request().method() === 'POST'
  );
  await page.getByTestId('request-form-submit').click();
  const submitResponse = await submitPromise;
  expect(submitResponse.ok(), `submit: ${await submitResponse.text()}`).toBeTruthy();
});

test('redirects the legacy request-type query param to the request route', async ({
  page,
  request,
}) => {
  await authenticateAdminRequest(request);

  const stamp = Date.now();
  const slug = `e2e-request-legacy-${stamp}`;
  const channel = await createPortalChannel(request, { slug, name: `Legacy ${stamp}` });
  const rt = await createSimpleRequestType(request, channel, {
    name: `Legacy form ${stamp}`,
  });
  await attachRequestTypesToSection(request, channel, [rt.id]);

  await page.goto(`/portal/${slug}?request-type=${rt.id}`);

  await expect(page).toHaveURL(new RegExp(`/portal/${slug}/request/${rt.id}$`));
  await expect(page.locator('#request-title')).toBeVisible();
});

test('asset report row action links to the prefilled request form', async ({ page, request }) => {
  await authenticateAdminRequest(request);

  const stamp = Date.now();
  const slug = `e2e-row-action-${stamp}`;
  const channel = await createPortalChannel(request, { slug, name: `Row action ${stamp}` });
  const rt = await createSimpleRequestType(request, channel, {
    name: `Row form ${stamp}`,
  });

  const setResponse = await request.post('/api/v2/asset-sets', {
    headers: SEC_FETCH,
    data: { name: `Row action set ${stamp}` },
  });
  expect(setResponse.ok(), `create asset set: ${await setResponse.text()}`).toBeTruthy();
  const assetSet = (await setResponse.json()).data;

  const typeResponse = await request.post(`/api/v2/asset-sets/${assetSet.id}/types`, {
    headers: SEC_FETCH,
    data: { name: `Row server ${stamp}` },
  });
  expect(typeResponse.ok(), `create asset type: ${await typeResponse.text()}`).toBeTruthy();
  const assetType = (await typeResponse.json()).data;

  const assetTag = `RA-${stamp}`;
  const assetResponse = await request.post(`/api/v2/asset-sets/${assetSet.id}/assets`, {
    headers: SEC_FETCH,
    data: {
      title: `Asset ${stamp}`,
      asset_tag: assetTag,
      asset_type_id: assetType.id,
    },
  });
  expect(assetResponse.ok(), `create asset: ${await assetResponse.text()}`).toBeTruthy();
  const assetBody = await assetResponse.json();
  const asset = assetBody.data ?? assetBody;

  const reportResponse = await request.post(`/api/channels/${channel.channelId}/asset-reports`, {
    headers: SEC_FETCH,
    data: {
      name: `Row action report ${stamp}`,
      asset_set_id: assetSet.id,
      cql_query: `asset_tag = "${assetTag}"`,
      icon: 'Table2',
      color: '#123456',
      column_config: ['title', 'asset_tag'],
      run_mode: 'direct',
      config: JSON.stringify({
        row_actions: [
          {
            id: 'ra1',
            label: `Report an issue ${stamp}`,
            request_type_id: rt.id,
            target_field: 'title',
            source: 'asset_tag',
          },
        ],
      }),
    },
  });
  expect(reportResponse.ok(), `create report: ${await reportResponse.text()}`).toBeTruthy();
  const report = await reportResponse.json();

  const configResponse = await request.put(`/api/channels/${channel.channelId}/config`, {
    headers: SEC_FETCH,
    data: {
      config: {
        portal_sections: [
          {
            id: `row-section-${stamp}`,
            title: '',
            subtitle: '',
            display_order: 0,
            request_type_ids: [],
            asset_report_ids: [report.id],
          },
        ],
      },
    },
  });
  expect(configResponse.ok(), `configure section: ${await configResponse.text()}`).toBeTruthy();

  await page.goto(`/portal/${slug}`);
  await expect(page.getByTestId('portal-page')).toHaveAttribute('data-ready', 'true');

  const link = page.getByTestId(`asset-report-action-${asset.id}-ra1`);
  await expect(link).toBeVisible();
  await link.click();

  await expect(page).toHaveURL(
    new RegExp(`/portal/${slug}/request/${rt.id}\\?prefill\\.title=${assetTag}$`)
  );
  await expect(page.locator('#request-title')).toHaveValue(assetTag);
});
