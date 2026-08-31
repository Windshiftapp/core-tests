import { expect, test } from '../fixtures/context-path';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

test('asset UI creates, preserves, changes, and clears asset tags', async ({ request, page }) => {
  const stamp = Date.now();
  const originalTag = `TAG-${stamp}`;
  const changedTag = `TAG-${stamp}-changed`;

  const setResponse = await request.post('/api/asset-sets', {
    headers: SEC_FETCH,
    data: { name: `Tagged assets ${stamp}` },
  });
  expect(setResponse.ok(), `create asset set: ${await setResponse.text()}`).toBeTruthy();
  const assetSet = await setResponse.json();

  const typeResponse = await request.post(`/api/asset-sets/${assetSet.id}/types`, {
    headers: SEC_FETCH,
    data: { name: `Tagged server ${stamp}` },
  });
  expect(typeResponse.ok(), `create asset type: ${await typeResponse.text()}`).toBeTruthy();

  await page.goto('/assets');
  await page.locator('#asset-set-select').click();
  await page.locator(`#asset-set-select-option-${assetSet.id}`).click();
  await page.getByTestId('asset-create').click();
  await page.locator('#asset-title-input').fill(`Tagged asset ${stamp}`);
  await page.locator('#asset-tag-input').fill(originalTag);
  await expect(page.locator('#asset-tag-input')).toHaveValue(originalTag);
  await page.locator('#asset-tag-input').blur();
  const createPromise = page.waitForResponse(
    (response) =>
      response.url().endsWith(`/api/asset-sets/${assetSet.id}/assets`) &&
      response.request().method() === 'POST'
  );
  await page.getByTestId('asset-submit').click();
  const createResponse = await createPromise;
  expect(createResponse.ok()).toBeTruthy();
  expect(createResponse.request().postDataJSON().asset_tag).toBe(originalTag);
  const asset = await createResponse.json();

  await page.goto(`/assets/${asset.id}`);
  await page.getByTestId('asset-edit').click();
  await expect(page.locator('#asset-tag-input')).toHaveValue(originalTag);
  await page.locator('#asset-title-input').fill(`Tagged asset renamed ${stamp}`);
  const preservePromise = page.waitForResponse(
    (response) =>
      response.url().endsWith(`/api/assets/${asset.id}`) && response.request().method() === 'PUT'
  );
  await page.getByTestId('asset-submit').click();
  const preserveResponse = await preservePromise;
  expect(preserveResponse.ok()).toBeTruthy();
  expect(preserveResponse.request().postDataJSON().asset_tag).toBe(originalTag);
  expect((await preserveResponse.json()).asset_tag).toBe(originalTag);
  await expect(page.getByTestId('asset-submit')).toHaveCount(0);

  await page.getByTestId('asset-edit').click();
  await expect(page.locator('#asset-tag-input')).toHaveValue(originalTag);
  await page.locator('#asset-tag-input').fill(changedTag);
  const changePromise = page.waitForResponse(
    (response) =>
      response.url().endsWith(`/api/assets/${asset.id}`) && response.request().method() === 'PUT'
  );
  await page.getByTestId('asset-submit').click();
  const changeResponse = await changePromise;
  expect(changeResponse.ok()).toBeTruthy();
  expect(changeResponse.request().postDataJSON().asset_tag).toBe(changedTag);
  expect((await changeResponse.json()).asset_tag).toBe(changedTag);
  await expect(page.getByTestId('asset-submit')).toHaveCount(0);

  await page.getByTestId('asset-edit').click();
  await expect(page.locator('#asset-tag-input')).toHaveValue(changedTag);
  await page.locator('#asset-tag-input').fill('');
  await expect(page.locator('#asset-tag-input')).toHaveValue('');
  const clearPromise = page.waitForResponse(
    (response) =>
      response.url().endsWith(`/api/assets/${asset.id}`) && response.request().method() === 'PUT'
  );
  await page.getByTestId('asset-submit').click();
  const clearResponse = await clearPromise;
  expect(clearResponse.ok()).toBeTruthy();
  expect(clearResponse.request().postDataJSON().asset_tag).toBe('');
  expect((await clearResponse.json()).asset_tag ?? '').toBe('');

  const storedResponse = await request.get(`/api/assets/${asset.id}`, {
    headers: SEC_FETCH,
  });
  expect(storedResponse.ok()).toBeTruthy();
  expect((await storedResponse.json()).asset_tag ?? '').toBe('');

  // Search is part of the browser contract, not merely an API capability.
  // Re-enter the set from a fresh page and prove both the matching and empty
  // result states against the asset persisted above.
  await page.goto('/assets');
  await page.locator('#asset-set-select').click();
  await page.locator(`#asset-set-select-option-${assetSet.id}`).click();

  let releaseMatchingResponse!: () => void;
  let matchingRequestStarted!: () => void;
  let matchingResponseReleased!: () => void;
  const matchingRequest = new Promise<void>((resolve) => {
    matchingRequestStarted = resolve;
  });
  const matchingRelease = new Promise<void>((resolve) => {
    releaseMatchingResponse = resolve;
  });
  const matchingReleased = new Promise<void>((resolve) => {
    matchingResponseReleased = resolve;
  });
  await page.route(
    (url) =>
      url.pathname === `/api/asset-sets/${assetSet.id}/assets` &&
      (url.searchParams.get('ql') ?? '').includes(`renamed ${stamp}`),
    async (route) => {
      const response = await route.fetch();
      matchingRequestStarted();
      await matchingRelease;
      await route.fulfill({ response });
      matchingResponseReleased();
    }
  );

  await page.getByTestId('asset-search').fill(`renamed ${stamp}`);
  await matchingRequest;
  await page.getByTestId('asset-search').fill(`no-match-${stamp}`);
  await expect(page.getByTestId('asset-row')).toHaveCount(0);
  releaseMatchingResponse();
  await matchingReleased;
  await expect(page.getByTestId('asset-row')).toHaveCount(0);
});
