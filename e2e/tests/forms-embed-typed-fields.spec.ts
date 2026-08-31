import { createCustomFieldViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const SELECT_OPTIONS = JSON.stringify({
  next_id: 4,
  items: [
    { id: 1, label: 'Low' },
    { id: 2, label: 'Medium' },
    { id: 3, label: 'High' },
  ],
});

test('built form widget submits supported custom and virtual typed fields', async ({
  request,
  page,
}) => {
  const stamp = Date.now();
  const slug = `embed-types-${stamp}`;
  const workspace = await createWorkspaceViaAPI(request, generateWorkspace(`embed-types-${stamp}`));
  const itemTypesResponse = await request.get('/api/item-types', {
    headers: SEC_FETCH,
  });
  expect(itemTypesResponse.ok()).toBeTruthy();
  const itemTypes = await itemTypesResponse.json();
  const itemTypeID = itemTypes[0]?.id;
  expect(itemTypeID).toBeGreaterThan(0);

  const definitions = await Promise.all([
    createCustomFieldViaAPI(request, {
      name: `Embed select ${stamp}`,
      field_type: 'select',
      options: SELECT_OPTIONS,
    }),
    createCustomFieldViaAPI(request, {
      name: `Embed multiselect ${stamp}`,
      field_type: 'multiselect',
      options: SELECT_OPTIONS,
    }),
    createCustomFieldViaAPI(request, {
      name: `Embed date ${stamp}`,
      field_type: 'date',
    }),
    createCustomFieldViaAPI(request, {
      name: `Embed number ${stamp}`,
      field_type: 'number',
    }),
    createCustomFieldViaAPI(request, {
      name: `Embed textarea ${stamp}`,
      field_type: 'textarea',
    }),
  ]);

  const screenResponse = await request.post('/api/screens', {
    headers: SEC_FETCH,
    data: { name: `Embed typed fields ${stamp}` },
  });
  expect(screenResponse.ok(), `create screen: ${await screenResponse.text()}`).toBeTruthy();
  const screen = await screenResponse.json();
  const screenFieldsResponse = await request.put(`/api/screens/${screen.id}/fields`, {
    headers: SEC_FETCH,
    data: definitions.map((field, index) => ({
      field_type: 'custom',
      field_identifier: String(field.id),
      display_order: index,
      is_required: false,
      field_width: 'full',
    })),
  });
  expect(
    screenFieldsResponse.ok(),
    `configure screen: ${await screenFieldsResponse.text()}`
  ).toBeTruthy();

  const configResponse = await request.post('/api/configuration-sets', {
    headers: SEC_FETCH,
    data: {
      name: `Embed typed config ${stamp}`,
      workspace_ids: [workspace.id],
      create_screen_id: screen.id,
      edit_screen_id: screen.id,
      view_screen_id: screen.id,
      item_type_configs: [
        {
          item_type_id: itemTypeID,
          create_screen_id: screen.id,
          edit_screen_id: screen.id,
          view_screen_id: screen.id,
        },
      ],
    },
  });
  expect(
    configResponse.ok(),
    `create configuration set: ${await configResponse.text()}`
  ).toBeTruthy();

  const channelResponse = await request.post('/api/channels', {
    headers: SEC_FETCH,
    data: {
      name: `Embed form ${stamp}`,
      type: 'form',
      direction: 'inbound',
      status: 'disabled',
      slug,
    },
  });
  expect(channelResponse.ok(), `create form channel: ${await channelResponse.text()}`).toBeTruthy();
  const channel = await channelResponse.json();
  const channelConfigResponse = await request.put(`/api/channels/${channel.id}/config`, {
    headers: SEC_FETCH,
    data: {
      config: {
        form_slug: slug,
        form_workspace_ids: [workspace.id],
        form_theme: 'light',
      },
    },
  });
  expect(
    channelConfigResponse.ok(),
    `configure form channel: ${await channelConfigResponse.text()}`
  ).toBeTruthy();

  const formResponse = await request.post(`/api/channels/${channel.id}/request-types`, {
    headers: SEC_FETCH,
    data: {
      name: `Typed widget form ${stamp}`,
      item_type_id: itemTypeID,
      workspace_id: workspace.id,
      is_active: true,
    },
  });
  expect(formResponse.ok(), `create form: ${await formResponse.text()}`).toBeTruthy();
  const form = await formResponse.json();

  const fields = [
    {
      field_identifier: 'title',
      field_type: 'default',
      display_order: 0,
      is_required: true,
      step_number: 1,
    },
    ...definitions.map((field, index) => ({
      field_identifier: String(field.id),
      field_type: 'custom',
      display_order: index + 1,
      is_required: true,
      step_number: 1,
    })),
    {
      field_identifier: 'virtual_confirmed',
      field_type: 'virtual',
      display_name: 'Virtual confirmation',
      virtual_field_type: 'checkbox',
      display_order: definitions.length + 1,
      is_required: false,
      step_number: 1,
    },
    {
      field_identifier: 'virtual_priority',
      field_type: 'virtual',
      display_name: 'Virtual priority',
      virtual_field_type: 'select',
      virtual_field_options: JSON.stringify([
        { value: 'urgent', label: 'Urgent' },
        { value: 'routine', label: 'Routine' },
      ]),
      display_order: definitions.length + 2,
      is_required: true,
      step_number: 1,
    },
  ];
  const fieldsResponse = await request.put(
    `/api/channels/${channel.id}/request-types/${form.id}/fields`,
    { headers: SEC_FETCH, data: fields }
  );
  expect(fieldsResponse.ok(), `configure form fields: ${await fieldsResponse.text()}`).toBeTruthy();
  const toggleResponse = await request.put(`/api/channels/${channel.id}/toggle`, {
    headers: SEC_FETCH,
  });
  expect(toggleResponse.ok(), `enable form channel: ${await toggleResponse.text()}`).toBeTruthy();

  const baseUrl = BASE_URL.replace(/\/$/, '');
  await page.goto('/login');
  await page.setContent(
    `<main><div id="embed-target"></div></main>
		<script
			src="${baseUrl}/embed/windshift-forms.js"
			data-target="embed-target"
			data-base-url="${baseUrl}"
			data-slug="${slug}"
		></script>`
  );

  await page.locator('#wsf-title').fill(`Widget submission ${stamp}`);
  await page.locator(`#wsf-${definitions[0].id}`).selectOption('2');
  await page.locator(`#wsf-${definitions[1].id}-option-0`).check();
  await page.locator(`#wsf-${definitions[1].id}-option-2`).check();
  await page.locator(`#wsf-${definitions[2].id}`).fill('2026-07-19');
  await page.locator(`#wsf-${definitions[3].id}`).fill('42.5');
  await page.locator(`#wsf-${definitions[4].id}`).fill('Typed textarea value');
  await page.locator('#wsf-virtual_confirmed').check();
  await page.locator('#wsf-virtual_priority').selectOption('urgent');

  const submitPromise = page.waitForResponse(
    (response) =>
      response.url().endsWith(`/api/forms/${slug}/submit`) && response.request().method() === 'POST'
  );
  await page.locator('#wsf-submit').click();
  const submitResponse = await submitPromise;
  expect(submitResponse.ok(), `widget submit: ${await submitResponse.text()}`).toBeTruthy();
  const payload = submitResponse.request().postDataJSON();
  expect(payload.custom_fields).toEqual({
    [String(definitions[0].id)]: 2,
    [String(definitions[1].id)]: [1, 3],
    [String(definitions[2].id)]: '2026-07-19',
    [String(definitions[3].id)]: 42.5,
    [String(definitions[4].id)]: 'Typed textarea value',
    virtual_confirmed: true,
    virtual_priority: 'urgent',
  });
  await expect(page.locator('#wsf-success')).toBeVisible();
});
