import { createItemViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { type APIRequestContext, expect, externalPath, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

async function createPublicForm(request: APIRequestContext, stamp: number) {
  const slug = `public-form-journey-${process.pid}-${stamp}`;
  const workspace = await createWorkspaceViaAPI(request, generateWorkspace(`public-form-${stamp}`));
  const itemTypesResponse = await request.get('/api/item-types', {
    headers: SEC_FETCH,
  });
  expect(itemTypesResponse.ok()).toBeTruthy();
  const itemTypes = (await itemTypesResponse.json()) as Array<{ id: number }>;
  expect(itemTypes[0]?.id).toBeGreaterThan(0);

  const channelResponse = await request.post('/api/channels', {
    headers: SEC_FETCH,
    data: {
      name: `Public form journey ${stamp}`,
      type: 'form',
      direction: 'inbound',
      status: 'disabled',
      slug,
    },
  });
  expect(channelResponse.ok(), `create form channel: ${await channelResponse.text()}`).toBeTruthy();
  const channel = await channelResponse.json();

  const configResponse = await request.put(`/api/channels/${channel.id}/config`, {
    headers: SEC_FETCH,
    data: {
      config: {
        form_slug: slug,
        form_workspace_ids: [workspace.id],
        form_theme: 'light',
        form_success_message: `Submission accepted ${stamp}`,
      },
    },
  });
  expect(
    configResponse.ok(),
    `configure form channel: ${await configResponse.text()}`
  ).toBeTruthy();

  const formResponse = await request.post(`/api/channels/${channel.id}/request-types`, {
    headers: SEC_FETCH,
    data: {
      name: `Anonymous request ${stamp}`,
      item_type_id: itemTypes[0].id,
      workspace_id: workspace.id,
      is_active: true,
    },
  });
  expect(formResponse.ok(), `create public form: ${await formResponse.text()}`).toBeTruthy();
  const form = await formResponse.json();

  const fieldsResponse = await request.put(
    `/api/channels/${channel.id}/request-types/${form.id}/fields`,
    {
      headers: SEC_FETCH,
      data: [
        {
          field_identifier: 'title',
          field_type: 'default',
          display_name: 'Request title',
          display_order: 0,
          is_required: true,
          step_number: 1,
        },
        {
          field_identifier: 'description',
          field_type: 'default',
          display_name: 'Request details',
          display_order: 1,
          is_required: true,
          step_number: 1,
        },
      ],
    }
  );
  expect(
    fieldsResponse.ok(),
    `configure public form fields: ${await fieldsResponse.text()}`
  ).toBeTruthy();

  const toggleResponse = await request.put(`/api/channels/${channel.id}/toggle`, {
    headers: SEC_FETCH,
  });
  expect(toggleResponse.ok(), `enable public form: ${await toggleResponse.text()}`).toBeTruthy();

  return { slug, workspace, channel, form };
}

test('multi-form public channels expose stable direct form URLs', async ({ request, browser }) => {
  const stamp = Date.now();
  const { slug, workspace, channel, form } = await createPublicForm(request, stamp);
  const directFormName = `Direct request ${stamp}`;

  const secondFormResponse = await request.post(`/api/channels/${channel.id}/request-types`, {
    headers: SEC_FETCH,
    data: {
      name: directFormName,
      item_type_id: form.item_type_id,
      workspace_id: workspace.id,
      is_active: true,
    },
  });
  expect(
    secondFormResponse.ok(),
    `create second public form: ${await secondFormResponse.text()}`
  ).toBeTruthy();
  const secondForm = await secondFormResponse.json();

  const fieldsResponse = await request.put(
    `/api/channels/${channel.id}/request-types/${secondForm.id}/fields`,
    {
      headers: SEC_FETCH,
      data: [
        {
          field_identifier: 'title',
          field_type: 'default',
          display_name: 'Request title',
          display_order: 0,
          is_required: true,
          step_number: 1,
        },
      ],
    }
  );
  expect(
    fieldsResponse.ok(),
    `configure second public form fields: ${await fieldsResponse.text()}`
  ).toBeTruthy();

  const context = await browser.newContext({
    baseURL: BASE_URL,
    storageState: { cookies: [], origins: [] },
  });
  const publicPage = await context.newPage();

  await publicPage.goto(`/forms/${slug}/${secondForm.id}`);
  const publicForm = publicPage.getByTestId('public-form-page');
  await expect(publicForm).toHaveAttribute('data-ready', 'true');
  await expect(publicPage.getByTestId('public-form-title')).toHaveText(directFormName);
  await expect(publicPage).toHaveURL(new RegExp(`/forms/${slug}/${secondForm.id}$`));

  await publicPage.getByTestId('public-form-back').click();
  await expect(publicPage).toHaveURL(new RegExp(`/forms/${slug}$`));
  await expect(publicPage.getByTestId(`public-form-option-${form.id}`)).toBeVisible();

  await publicPage.getByTestId(`public-form-option-${form.id}`).click();
  await expect(publicPage).toHaveURL(new RegExp(`/forms/${slug}/${form.id}$`));
  await expect(publicPage.getByTestId('public-form-title')).toHaveText(form.name);

  await publicPage.goBack();
  await expect(publicPage).toHaveURL(new RegExp(`/forms/${slug}$`));
  await expect(publicPage.getByTestId(`public-form-option-${form.id}`)).toBeVisible();
  await expect(publicPage.getByTestId('public-form-title')).toHaveCount(0);

  await publicPage.goForward();
  await expect(publicPage).toHaveURL(new RegExp(`/forms/${slug}/${form.id}$`));
  await expect(publicPage.getByTestId('public-form-title')).toHaveText(form.name);

  await context.close();
});

test('public form drafts restore values and step, then start fresh', async ({
  request,
  browser,
}) => {
  const stamp = Date.now();
  const { slug, channel, form } = await createPublicForm(request, stamp);

  const fieldsResponse = await request.put(
    `/api/channels/${channel.id}/request-types/${form.id}/fields`,
    {
      headers: SEC_FETCH,
      data: [
        {
          field_identifier: 'title',
          field_type: 'default',
          display_name: 'Request title',
          display_order: 0,
          is_required: true,
          step_number: 1,
        },
        {
          field_identifier: 'description',
          field_type: 'default',
          display_name: 'Request details',
          display_order: 1,
          is_required: true,
          step_number: 2,
        },
      ],
    }
  );
  expect(
    fieldsResponse.ok(),
    `configure multi-step public form: ${await fieldsResponse.text()}`
  ).toBeTruthy();

  const context = await browser.newContext({
    baseURL: BASE_URL,
    storageState: { cookies: [], origins: [] },
  });
  const publicPage = await context.newPage();
  await publicPage.goto(`/forms/${slug}/${form.id}`);
  await expect(publicPage.getByTestId('public-form-page')).toHaveAttribute('data-ready', 'true');

  const draftTitle = `Saved browser draft ${stamp}`;
  await publicPage.locator('#form-title').fill(draftTitle);
  await publicPage.getByTestId('public-form-submit').click();
  await expect(publicPage.locator('#form-description')).toBeVisible();
  await expect(publicPage.getByTestId('public-form-draft-status')).toHaveText('Draft saved');

  await publicPage.reload();
  await expect(publicPage.getByTestId('public-form-draft-resume')).toBeVisible();
  await expect(publicPage.locator('#form-description')).toBeVisible();
  await publicPage.getByTestId('public-form-back-step').click();
  await expect(publicPage.locator('#form-title')).toHaveValue(draftTitle);

  await publicPage.getByTestId('public-form-draft-start-fresh').click();
  await expect(publicPage.getByTestId('public-form-draft-resume')).toHaveCount(0);
  await expect(publicPage.locator('#form-title')).toHaveValue('');

  await context.close();
});

test('authentication-required hosted forms sign in and return to the same form', async ({
  request,
  browser,
}) => {
  const stamp = Date.now();
  const { slug, form } = await createPublicForm(request, stamp);
  const configResponse = await request.put(`/api/request-types/${form.id}/config`, {
    headers: SEC_FETCH,
    data: {
      require_auth: true,
      allow_attachments: false,
      submit_button_text: 'Send request',
    },
  });
  expect(
    configResponse.ok(),
    `require public form authentication: ${await configResponse.text()}`
  ).toBeTruthy();

  const context = await browser.newContext({
    baseURL: BASE_URL,
    storageState: { cookies: [], origins: [] },
  });
  const publicPage = await context.newPage();
  const formPath = `/forms/${slug}/${form.id}`;
  const externalFormPath = externalPath(formPath);
  await publicPage.goto(formPath);
  await expect(publicPage.getByTestId('form-auth-required')).toBeVisible();
  await expect(publicPage.locator('#form-title')).toHaveCount(0);

  await publicPage.getByTestId('public-form-auth-action').click();
  await expect(publicPage).toHaveURL(
    new RegExp(`/login\\?return_to=${encodeURIComponent(externalFormPath)}$`)
  );
  await expect(publicPage.getByTestId('login-dialog')).toBeVisible();
  const fidoProbe = publicPage.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      response.url().endsWith('/api/auth/webauthn/login/start')
  );
  const usernameInput = publicPage.locator('#emailOrUsername');
  const passwordInput = publicPage.locator('#password');
  await usernameInput.fill('admin');
  await passwordInput.focus();
  await fidoProbe;
  await passwordInput.fill('TestPass123!');
  await expect(usernameInput).toHaveValue('admin');
  await expect(passwordInput).toHaveValue('TestPass123!');
  await expect(publicPage.getByTestId('login-submit')).toBeEnabled();
  await publicPage.getByTestId('login-submit').click();

  await expect(publicPage).toHaveURL(new RegExp(`${formPath}$`));
  await expect(publicPage.getByTestId('form-auth-required')).toHaveCount(0);
  await expect(publicPage.locator('#form-title')).toBeVisible();

  await context.close();
});

test('admin preview renders current form fields without submitting', async ({ request, page }) => {
  const stamp = Date.now();
  const { channel, form } = await createPublicForm(request, stamp);
  await page.goto(`/admin/channels/${channel.id}/forms`);
  await page.getByTestId(`form-row-${form.id}`).click();
  await expect(page.getByTestId('form-builder-preview-btn')).toBeVisible();

  let submitRequests = 0;
  page.on('request', (outgoing) => {
    if (
      outgoing.method() === 'POST' &&
      outgoing.url().includes('/api/forms/') &&
      outgoing.url().endsWith('/submit')
    ) {
      submitRequests += 1;
    }
  });

  await page.getByTestId('form-builder-preview-btn').click();
  await expect(page.getByTestId('form-preview-modal')).toBeVisible();
  await expect(page.locator('#form-title')).toBeVisible();
  await page.locator('#form-title').fill(`Preview only ${stamp}`);
  await expect(page.getByTestId('public-form-submit')).toBeDisabled();
  expect(submitRequests).toBe(0);
});

test('anonymous public form retains an attachment after failure and creates it exactly once on retry', async ({
  request,
  browser,
  page,
}) => {
  const stamp = Date.now();
  const { slug, workspace, channel, form } = await createPublicForm(request, stamp);
  const title = `Anonymous retry ${stamp}`;
  const description = `Retained details ${stamp}`;
  const attachmentName = `anonymous-evidence-${stamp}.txt`;
  let submitAttempts = 0;

  const context = await browser.newContext({
    baseURL: BASE_URL,
    storageState: { cookies: [], origins: [] },
  });
  const publicPage = await context.newPage();
  await publicPage.route(`**/api/forms/${slug}/submit`, async (route) => {
    submitAttempts += 1;
    if (submitAttempts === 1) {
      await route.fulfill({
        status: 503,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'Temporary form outage' }),
      });
      return;
    }
    await route.continue();
  });

  await publicPage.goto(`/forms/${slug}`);
  const publicForm = publicPage.getByTestId('public-form-page');
  await expect(publicForm).toHaveAttribute('data-ready', 'true');
  await expect(publicPage.getByTestId('public-form-attachments')).toHaveCount(0);

  await page.goto(`/admin/channels/${channel.id}/forms`);
  await page.getByTestId(`form-row-${form.id}`).click();
  await page.getByTestId('form-builder-settings-btn').click();
  const allowAttachments = page.getByTestId('form-allow-attachments');
  await expect(allowAttachments).not.toBeChecked();
  await allowAttachments.check();
  const configSaved = page.waitForResponse(
    (response) =>
      response.url().endsWith(`/api/request-types/${form.id}/config`) &&
      response.request().method() === 'PUT' &&
      response.status() === 200
  );
  await page.getByTestId('form-builder-save-btn').click();
  await configSaved;

  await publicPage.reload();
  await expect(publicForm).toHaveAttribute('data-ready', 'true');
  await expect(publicPage.getByTestId('public-form-attachments')).toBeVisible();

  await publicPage.locator('#form-title').fill('   ');
  await publicPage.locator('#form-description').fill(description);
  await publicPage.locator('#form-title').press('Enter');
  await expect(publicForm).toContainText('Request title is required');
  expect(submitAttempts).toBe(0);

  await publicPage.locator('#form-title').fill(title);
  await publicPage.getByTestId('public-form-attachments').setInputFiles({
    name: attachmentName,
    mimeType: 'text/plain',
    buffer: Buffer.from(`Anonymous attachment ${stamp}\n`),
  });
  await expect(publicPage.getByTestId('public-form-attachment-list')).toContainText(attachmentName);
  await publicPage.locator('#form-title').press('Enter');
  await expect(publicForm).toContainText('Temporary form outage');
  await expect(publicPage.locator('#form-title')).toHaveValue(title);
  await expect(publicPage.locator('#form-description')).toHaveValue(description);
  await expect(publicPage.getByTestId('public-form-attachment-list')).toContainText(attachmentName);
  expect(submitAttempts).toBe(1);

  const successfulSubmission = publicPage.waitForResponse(
    (response) =>
      response.url().endsWith(`/api/forms/${slug}/submit`) &&
      response.request().method() === 'POST' &&
      response.status() === 201
  );
  await publicPage.locator('#form-title').press('Enter');
  await successfulSubmission;
  await expect(publicPage.getByTestId('public-form-success')).toContainText(
    `Submission accepted ${stamp}`
  );
  expect(submitAttempts).toBe(2);
  await publicPage.getByTestId('public-form-submit-another').click();
  await expect(publicPage.locator('#form-title')).toHaveValue('');
  await expect(publicPage.locator('#form-description')).toHaveValue('');

  const itemsResponse = await request.get(`/api/items?workspace_id=${workspace.id}&limit=100`, {
    headers: SEC_FETCH,
  });
  expect(itemsResponse.ok()).toBeTruthy();
  const itemsBody = await itemsResponse.json();
  const items = (itemsBody.items ?? itemsBody) as Array<{
    id: number;
    title: string;
  }>;
  const createdItems = items.filter((item) => item.title === title);
  expect(createdItems).toHaveLength(1);

  await page.goto(`/workspaces/${workspace.id}/items/${createdItems[0].id}`);
  await expect(page.getByTestId('item-detail-ready')).toBeVisible();
  await expect(page.getByTestId('item-attachment')).toHaveCount(1);
  await expect(page.getByTestId('item-attachment')).toContainText(attachmentName);

  await context.close();
});

test('anonymous public board filters cards, opens safe detail, and refreshes after revocation', async ({
  request,
  browser,
}) => {
  const stamp = Date.now();
  const workspace = await createWorkspaceViaAPI(
    request,
    generateWorkspace(`public-board-${stamp}`)
  );
  const includedTitle = `Public board visible ${stamp}`;
  const excludedTitle = `Public board hidden ${stamp}`;
  const publicComment = `Public board comment ${stamp}`;
  const privateComment = `Private board secret ${stamp}`;
  const included = await createItemViaAPI(request, workspace.id, {
    title: includedTitle,
    description: `Visible description ${stamp}`,
  });
  await createItemViaAPI(request, workspace.id, {
    title: excludedTitle,
    description: `Excluded description ${stamp}`,
  });

  for (const [content, isPrivate] of [
    [publicComment, false],
    [privateComment, true],
  ] as const) {
    const commentResponse = await request.post(`/api/items/${included.id}/comments`, {
      headers: SEC_FETCH,
      data: { content, is_private: isPrivate },
    });
    expect(
      commentResponse.ok(),
      `create ${isPrivate ? 'private' : 'public'} comment: ${await commentResponse.text()}`
    ).toBeTruthy();
  }

  const slug = `public-board-journey-${stamp}`;
  const collectionResponse = await request.post('/api/collections', {
    headers: SEC_FETCH,
    data: {
      name: `Public consumer board ${stamp}`,
      description: `Anonymous board ${stamp}`,
      ql_query: `workspaceKey = "${workspace.key}" AND title ~ "${includedTitle}"`,
      workspace_id: workspace.id,
      is_public: true,
      public_slug: slug,
    },
  });
  expect(
    collectionResponse.status(),
    `create public collection: ${await collectionResponse.text()}`
  ).toBe(201);
  const collection = await collectionResponse.json();

  const context = await browser.newContext({
    baseURL: BASE_URL,
    storageState: { cookies: [], origins: [] },
  });
  const page = await context.newPage();
  await page.clock.install();
  await page.goto(`/board/${slug}`);
  const board = page.getByTestId('public-board-page');
  await expect(board).toHaveAttribute('data-ready', 'true');
  const cards = page.getByTestId('public-board-card');
  await expect(cards).toHaveCount(1);
  await expect(cards).toContainText(includedTitle);
  await expect(board).not.toContainText(excludedTitle);

  await cards.click();
  const detail = page.getByTestId('public-board-item-detail');
  await expect(detail).toBeVisible();
  await expect(detail).toContainText(includedTitle);
  await expect(detail.getByTestId('public-board-comment')).toContainText(publicComment);
  await expect(detail).not.toContainText(privateComment);

  const revokeResponse = await request.put(`/api/collections/${collection.id}/public`, {
    headers: SEC_FETCH,
    data: { is_public: false, public_slug: slug },
  });
  expect(revokeResponse.ok(), `revoke public board: ${await revokeResponse.text()}`).toBeTruthy();

  const refreshResponse = page.waitForResponse(
    (response) => response.url().endsWith(`/api/public/board/${slug}`) && response.status() === 404
  );
  await page.clock.fastForward(60_000);
  await refreshResponse;
  await expect(page.getByTestId('public-board-not-found')).toBeVisible();
  await expect(page.getByTestId('public-board-card')).toHaveCount(0);

  await context.close();
});
