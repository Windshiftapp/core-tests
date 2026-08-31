import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';

test('creates a usable form channel without a separate setup or activation step', async ({
  request,
  page,
}) => {
  const stamp = Date.now();
  const slug = `form-create-${process.pid}-${stamp}`;
  const workspace = await createWorkspaceViaAPI(request, generateWorkspace(`form-create-${stamp}`));
  await page.goto('/admin/channels/type/form');
  await page.getByTestId('channel-create-open').click();
  await expect(page.getByTestId('channel-type-form')).toHaveAttribute('aria-pressed', 'true');
  await page.locator('#channelName').fill(`Customer feedback ${stamp}`);
  await page.locator('#channelSlug').fill(slug);

  const channelCreated = page.waitForResponse(
    (response) => response.url().endsWith('/api/channels') && response.request().method() === 'POST'
  );
  await page.getByTestId('channel-create-confirm').click();
  const channelResponse = await channelCreated;
  expect(channelResponse.ok(), `create form channel: ${await channelResponse.text()}`).toBeTruthy();
  const channel = await channelResponse.json();

  await expect(page).toHaveURL(new RegExp(`/admin/channels/${channel.id}/forms$`));
  await expect(page.getByTestId('form-create-open')).toBeVisible();
  await expect(page.getByTestId('channel-tab-integration')).toHaveCount(0);

  await page.getByTestId('form-create-open').click();
  await expect(page.locator('#form-description')).toHaveCount(0);
  await page.locator('#form-name').fill(`Feedback form ${stamp}`);
  await page.locator('#form-workspace').click();
  const itemTypesLoaded = page.waitForResponse(
    (response) =>
      response.url().includes('/api/item-types?configuration_set_id=') &&
      response.request().method() === 'GET'
  );
  await page.getByTestId(`form-workspace-${workspace.id}`).click();
  const itemTypesResponse = await itemTypesLoaded;
  expect(itemTypesResponse.ok()).toBeTruthy();
  const itemTypes = (await itemTypesResponse.json()) as Array<{ id: number }>;
  expect(itemTypes[0]?.id).toBeGreaterThan(0);
  await page.locator('#form-item-type').click();
  await page.getByTestId(`form-item-type-${itemTypes[0].id}`).click();

  const formCreated = page.waitForResponse(
    (response) =>
      response.url().endsWith(`/api/channels/${channel.id}/request-types`) &&
      response.request().method() === 'POST'
  );
  await page.getByTestId('form-create-confirm').click();
  const formResponse = await formCreated;
  expect(formResponse.ok(), `create form: ${await formResponse.text()}`).toBeTruthy();
  const form = await formResponse.json();

  await expect(page.getByTestId(`form-row-${form.id}`)).toBeVisible();
  await expect(page.getByTestId('channel-tab-integration')).toBeVisible();

  const publicPage = await page.context().newPage();
  await publicPage.goto(`/forms/${slug}`);
  await expect(publicPage.getByTestId('public-form-page')).toHaveAttribute('data-ready', 'true');
  await publicPage.close();
});
