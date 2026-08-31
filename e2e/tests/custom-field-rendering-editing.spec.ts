import {
  createCustomFieldViaAPI,
  createItemViaAPI,
  createWorkspaceViaAPI,
  deleteCustomFieldViaAPI,
} from '../fixtures/api-helpers';
import {
  type APIRequestContext,
  expect,
  type Locator,
  type Page,
  test,
} from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';
import { ItemPage } from '../pages/item.page';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

const LONG_TEXT_VALUE =
  'This custom field value is intentionally long enough to be truncated in the item sidebar.';
const LONG_URL_VALUE =
  'https://example.com/projects/windshift/custom-fields/this-is-a-long-path-that-must-remain-usable';

const SELECT_OPTIONS = {
  next_id: 4,
  items: [
    { id: 1, label: 'Low' },
    { id: 2, label: 'Medium' },
    { id: 3, label: 'High' },
  ],
};

test.describe('Custom field rendering and editing', () => {
  test('renders API-seeded values and edits scalar custom fields in item detail', async ({
    page,
    request,
  }) => {
    const suffix = `${Date.now()}-${Math.floor(Math.random() * 10000)}`;
    const createdFieldIds: number[] = [];

    const workspace = await createWorkspaceViaAPI(
      request,
      generateWorkspace(`cf-render-${suffix}`)
    );

    const screen = await createScreen(request, `E2E CF screen ${suffix}`);

    const fields = await Promise.all([
      createField(request, createdFieldIds, `E2E Text ${suffix}`, 'text'),
      createField(request, createdFieldIds, `E2E Textarea ${suffix}`, 'textarea'),
      createField(request, createdFieldIds, `E2E URL text ${suffix}`, 'text'),
      createField(request, createdFieldIds, `E2E Number ${suffix}`, 'number'),
      createField(request, createdFieldIds, `E2E Date ${suffix}`, 'date'),
      createField(
        request,
        createdFieldIds,
        `E2E Select ${suffix}`,
        'select',
        JSON.stringify(SELECT_OPTIONS)
      ),
      createField(
        request,
        createdFieldIds,
        `E2E Multiselect ${suffix}`,
        'multiselect',
        JSON.stringify(SELECT_OPTIONS)
      ),
    ]);
    const [
      textField,
      textareaField,
      urlTextField,
      numberField,
      dateField,
      selectField,
      multiselectField,
    ] = fields;

    await updateScreenFields(
      request,
      screen.id,
      fields.map((field, index) => ({
        field_type: 'custom',
        field_identifier: String(field.id),
        display_order: index,
        is_required: false,
        field_width: 'full',
      }))
    );

    const configSet = await createConfigurationSet(request, {
      name: `E2E CF config ${suffix}`,
      description: 'Exercises custom field item rendering/editing in browser',
      workspace_ids: [workspace.id],
      create_screen_id: screen.id,
      edit_screen_id: screen.id,
      view_screen_id: screen.id,
    });

    const item = await createItemViaAPI(request, workspace.id, {
      title: `E2E custom field item ${suffix}`,
      description: 'Seeded via API so the browser test focuses on rendering and inline editing.',
      custom_field_values: {
        [textField.id]: LONG_TEXT_VALUE,
        [textareaField.id]: 'Initial long value',
        [urlTextField.id]: LONG_URL_VALUE,
        [numberField.id]: 7.5,
        [dateField.id]: '2026-05-15',
        [selectField.id]: 2,
        [multiselectField.id]: [1, 3],
      },
    });

    try {
      const itemPage = new ItemPage(page);
      await itemPage.gotoWorkspaceBacklog(String(workspace.id));
      await itemPage.openItemDetailModal(item.title);
      const dialog = page.getByTestId('item-detail');

      await expect(dialog).toContainText(`E2E Text ${suffix}`);
      await expect(dialog).toContainText(LONG_TEXT_VALUE);
      await expect(dialog).toContainText(`E2E Textarea ${suffix}`);
      await expect(dialog).toContainText('Initial long value');
      await expect(dialog).toContainText(`E2E URL text ${suffix}`);
      const urlLink = dialog.getByTestId(`item-custom-field-value-${urlTextField.id}`);
      await expect(urlLink).toHaveAttribute('href', LONG_URL_VALUE);
      await expect(urlLink).toHaveAttribute('target', '_blank');
      await expect(urlLink).toHaveAttribute('rel', /noopener/);
      await expect(dialog).toContainText(`E2E Number ${suffix}`);
      await expect(dialog).toContainText('7.5');
      await expect(dialog).toContainText(`E2E Date ${suffix}`);
      await expect(dialog).toContainText('May 15, 2026');
      await expect(dialog).toContainText(`E2E Select ${suffix}`);
      await expect(dialog).toContainText('Medium');
      await expect(dialog).toContainText(`E2E Multiselect ${suffix}`);
      await expect(dialog).toContainText('Low, High');

      const longTextValue = dialog.getByTestId(`item-custom-field-edit-${textField.id}`);
      await longTextValue.hover();
      await expect(page.getByTestId('tooltip')).toHaveText(LONG_TEXT_VALUE);

      await urlLink.hover();
      await expect(page.getByTestId('tooltip')).toHaveText(LONG_URL_VALUE);

      await editSingleLineTextField(page, dialog, textField.id, 'Edited text value');
      await expect(dialog).toContainText('Edited text value');

      await editTextLikeField(page, dialog, textareaField.id, 'Edited multiline value');
      await expect(dialog).toContainText('Edited multiline value');

      await editTextLikeField(page, dialog, numberField.id, '42.25');
      await expect(dialog).toContainText('42.25');

      await editTextLikeField(page, dialog, dateField.id, '2026-06-20');
      await expect(dialog).toContainText('Jun 20, 2026');

      const refreshed = await request.get(`/api/items/${item.id}`, {
        headers: SEC_FETCH,
      });
      expect(
        refreshed.ok(),
        `reload item: ${refreshed.status()} ${await refreshed.text()}`
      ).toBeTruthy();
      const refreshedItem = await refreshed.json();
      expect(refreshedItem.custom_field_values[String(textField.id)]).toBe('Edited text value');
      expect(refreshedItem.custom_field_values[String(textareaField.id)]).toBe(
        'Edited multiline value'
      );
      expect(refreshedItem.custom_field_values[String(numberField.id)]).toBe(42.25);
      expect(refreshedItem.custom_field_values[String(dateField.id)]).toBe('2026-06-20');
    } finally {
      await request
        .delete(`/api/configuration-sets/${configSet.id}`, {
          headers: SEC_FETCH,
        })
        .catch(() => {});
      await request.delete(`/api/screens/${screen.id}`, { headers: SEC_FETCH }).catch(() => {});
      for (const fieldId of createdFieldIds.reverse()) {
        await deleteCustomFieldViaAPI(request, fieldId).catch(() => {});
      }
    }
  });
});

async function createField(
  request: APIRequestContext,
  createdFieldIds: number[],
  name: string,
  fieldType: string,
  options?: string
) {
  const field = await createCustomFieldViaAPI(request, {
    name,
    field_type: fieldType,
    options,
    required: false,
  });
  createdFieldIds.push(field.id);
  return field;
}

async function createScreen(request: APIRequestContext, name: string) {
  const response = await request.post('/api/screens', {
    headers: SEC_FETCH,
    data: { name, description: 'E2E custom field rendering/editing screen' },
  });
  expect(
    response.ok(),
    `create screen: ${response.status()} ${await response.text()}`
  ).toBeTruthy();
  return response.json();
}

async function updateScreenFields(
  request: APIRequestContext,
  screenId: number,
  fields: Array<{
    field_type: string;
    field_identifier: string;
    display_order: number;
    is_required: boolean;
    field_width: string;
  }>
) {
  const response = await request.put(`/api/screens/${screenId}/fields`, {
    headers: SEC_FETCH,
    data: fields,
  });
  expect(
    response.ok(),
    `update screen fields: ${response.status()} ${await response.text()}`
  ).toBeTruthy();
}

async function createConfigurationSet(
  request: APIRequestContext,
  data: {
    name: string;
    description: string;
    workspace_ids: number[];
    create_screen_id: number;
    edit_screen_id: number;
    view_screen_id: number;
  }
) {
  const response = await request.post('/api/configuration-sets', {
    headers: SEC_FETCH,
    data,
  });
  expect(
    response.ok(),
    `create configuration set: ${response.status()} ${await response.text()}`
  ).toBeTruthy();
  return response.json();
}

async function editTextLikeField(page: Page, dialog: Locator, fieldId: number, value: string) {
  await dialog.getByTestId(`item-custom-field-edit-${fieldId}`).click();
  const input = dialog.getByTestId(`custom-field-input-${fieldId}`);
  await expect(input).toBeVisible();

  const responsePromise = page.waitForResponse(
    (res) => res.request().method() === 'PUT' && /\/api\/items\/\d+/.test(res.url())
  );
  await input.fill(value);
  const response = await responsePromise;
  expect(
    response.ok(),
    `custom field save: ${response.status()} ${await response.text()}`
  ).toBeTruthy();
  await expect(input).toBeHidden({ timeout: 10000 });
}

async function editSingleLineTextField(
  page: Page,
  dialog: Locator,
  fieldId: number,
  value: string
) {
  await dialog.getByTestId(`item-custom-field-edit-${fieldId}`).click();
  const input = dialog.getByTestId(`custom-field-input-${fieldId}`);
  await expect(input).toBeFocused();

  await input.press('ControlOrMeta+A');
  await input.pressSequentially(value);

  await expect(input).toBeFocused();
  await expect(input).toHaveValue(value);

  const responsePromise = page.waitForResponse(
    (res) => res.request().method() === 'PUT' && /\/api\/items\/\d+/.test(res.url())
  );
  await input.press('Enter');
  const response = await responsePromise;
  expect(
    response.ok(),
    `custom field save: ${response.status()} ${await response.text()}`
  ).toBeTruthy();
  await expect(input).toBeHidden({ timeout: 10000 });
}
