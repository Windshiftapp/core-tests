import { authenticateAdminRequest } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { createPortalChannel } from '../helpers/portal-setup';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

/**
 * Regression: dropping an asset report from the customize panel onto a portal
 * section must persist the section's asset_report_ids. The customize panel's
 * modal backdrop used to cover the section drop zone for the asset-reports
 * section, so the drop never reached PortalSections.
 */
test('drops an asset report onto a portal section from the customize panel', async ({
  page,
  request,
}) => {
  await authenticateAdminRequest(request);

  const stamp = Date.now();
  const slug = `e2e-asset-drop-${stamp}`;
  const channel = await createPortalChannel(request, {
    slug,
    name: `Asset drop ${stamp}`,
  });

  const itemTypesResponse = await request.get('/api/v2/item-types', {
    headers: SEC_FETCH,
  });
  expect(itemTypesResponse.ok()).toBeTruthy();
  const itemTypeId = (await itemTypesResponse.json()).data[0]?.id;
  expect(itemTypeId).toBeGreaterThan(0);

  const assetSetResponse = await request.post('/api/v2/asset-sets', {
    headers: SEC_FETCH,
    data: { name: `Asset drop set ${stamp}` },
  });
  expect(assetSetResponse.ok(), `create asset set: ${await assetSetResponse.text()}`).toBeTruthy();
  const assetSet = (await assetSetResponse.json()).data;

  // The set must carry a portal-access grant or its reports stay hidden from
  // the portal catalog.
  const portalAccessResponse = await request.put(`/api/v2/asset-sets/${assetSet.id}/portal-access`, {
    headers: SEC_FETCH,
    data: { enabled: true },
  });
  expect(portalAccessResponse.ok(), `grant portal access: ${await portalAccessResponse.text()}`).toBeTruthy();

  const reportResponse = await request.post(`/api/channels/${channel.channelId}/asset-reports`, {
    headers: SEC_FETCH,
    data: {
      name: `Drop report ${stamp}`,
      description: 'Dropped from the customize panel',
      asset_set_id: assetSet.id,
      cql_query: `title ~ "drop-${stamp}"`,
      icon: 'Table2',
      color: '#123456',
      column_config: ['title'],
      run_mode: 'form',
      item_type_id: itemTypeId,
      workspace_id: channel.workspaceId,
      config: JSON.stringify({
        require_auth: true,
        success_message: 'Dropped report complete',
        submit_button_text: 'Run dropped report',
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
            id: `drop-section-${stamp}`,
            title: 'Droppable',
            subtitle: '',
            display_order: 0,
            request_type_ids: [],
            asset_report_ids: [],
          },
        ],
      },
    },
  });
  expect(configResponse.ok(), `configure sections: ${await configResponse.text()}`).toBeTruthy();

  await page.goto(`/portal/${slug}`);
  await expect(page.getByTestId('portal-page')).toHaveAttribute('data-ready', 'true');

  await page.getByTestId('portal-settings-button').click();
  await page.getByTestId('portal-customize-button').click();
  await page.getByTestId('portal-edit-mode-toggle').click();
  await page.getByTestId('portal-customize-asset-reports-section').click();

  const reportCard = page.getByTestId('portal-customize-asset-report-card');
  // The draggable/drop-target registration runs after a short DOM-settle
  // timeout, so wait for the card to become a native drag source first.
  await expect(reportCard).toHaveAttribute('draggable', 'true');
  const dragHandle = page.getByTestId('portal-asset-report-drag-handle');
  await expect(dragHandle).toBeVisible();
  const dropZone = page.getByTestId('portal-section-drop-zone');
  await expect(dropZone).toBeVisible();

  const saved = page.waitForResponse(
    (response) =>
      response.request().method() === 'PUT' &&
      response.url().endsWith(`/api/channels/${channel.channelId}/config`) &&
      response.ok()
  );

  await dragHandle.dragTo(dropZone);

  await expect(page.locator(`#portal-asset-form-${report.id}`)).toBeVisible();
  await saved;

  // Reload to prove the drop was persisted, not just held in local state.
  await page.goto(`/portal/${slug}`);
  await expect(page.getByTestId('portal-page')).toHaveAttribute('data-ready', 'true');
  await expect(page.locator(`#portal-asset-form-${report.id}`)).toBeVisible();
});
