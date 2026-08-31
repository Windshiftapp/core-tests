import { deleteCustomFieldViaAPI, listCustomFieldsViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateCustomField } from '../fixtures/test-data';
import { CustomFieldsPage, FIELD_TYPE_LABELS } from '../pages/custom-fields.page';

/**
 * Custom Fields E2E Tests
 * Tests creating every supported custom field type via the admin UI.
 * Skips 'asset' (requires pre-existing asset set) and 'linking' (requires pre-existing link type).
 */

test.describe('Custom Fields', () => {
  let customFieldsPage: CustomFieldsPage;
  const createdFieldNames: string[] = [];

  test.beforeEach(async ({ page }) => {
    customFieldsPage = new CustomFieldsPage(page);
  });

  test.afterAll(async ({ request }) => {
    // Clean up all fields created during this test run
    const fields = await listCustomFieldsViaAPI(request);
    for (const name of createdFieldNames) {
      const field = fields.find((f: { name: string }) => f.name === name);
      if (field) {
        await deleteCustomFieldViaAPI(request, field.id);
      }
    }
  });

  test.describe('Create Custom Fields', () => {
    test('should create a text field', async () => {
      const data = generateCustomField('text');
      createdFieldNames.push(data.name);

      await customFieldsPage.goto();
      await customFieldsPage.createField({ name: data.name, type: 'text' });
      await customFieldsPage.verifyFieldExists(data.name);
      await customFieldsPage.verifyFieldType(data.name, FIELD_TYPE_LABELS.text);
    });

    test('should create a textarea field', async () => {
      const data = generateCustomField('textarea');
      createdFieldNames.push(data.name);

      await customFieldsPage.goto();
      await customFieldsPage.createField({ name: data.name, type: 'textarea' });
      await customFieldsPage.verifyFieldExists(data.name);
      await customFieldsPage.verifyFieldType(data.name, FIELD_TYPE_LABELS.textarea);
    });

    test('should create a select field with options', async () => {
      const data = generateCustomField('select');
      createdFieldNames.push(data.name);
      const options = ['Option A', 'Option B', 'Option C'];

      await customFieldsPage.goto();
      await customFieldsPage.createField({ name: data.name, type: 'select', options });
      await customFieldsPage.verifyFieldExists(data.name);
      await customFieldsPage.verifyFieldType(data.name, FIELD_TYPE_LABELS.select);

      // Reopen the field and confirm every option label persisted (not just
      // that the field exists with the right type).
      await customFieldsPage.openFieldForEdit(data.name);
      expect(await customFieldsPage.getOptionValues()).toEqual(options);
      await customFieldsPage.closeModal();
    });

    test('should create a multiselect field with options', async () => {
      const data = generateCustomField('multiselect');
      createdFieldNames.push(data.name);
      const options = ['Tag 1', 'Tag 2', 'Tag 3'];

      await customFieldsPage.goto();
      await customFieldsPage.createField({ name: data.name, type: 'multiselect', options });
      await customFieldsPage.verifyFieldExists(data.name);
      await customFieldsPage.verifyFieldType(data.name, FIELD_TYPE_LABELS.multiselect);

      await customFieldsPage.openFieldForEdit(data.name);
      expect(await customFieldsPage.getOptionValues()).toEqual(options);
      await customFieldsPage.closeModal();
    });

    test('should create a number field', async () => {
      const data = generateCustomField('number');
      createdFieldNames.push(data.name);

      await customFieldsPage.goto();
      await customFieldsPage.createField({ name: data.name, type: 'number' });
      await customFieldsPage.verifyFieldExists(data.name);
      await customFieldsPage.verifyFieldType(data.name, FIELD_TYPE_LABELS.number);
    });

    test('should create a date field', async () => {
      const data = generateCustomField('date');
      createdFieldNames.push(data.name);

      await customFieldsPage.goto();
      await customFieldsPage.createField({ name: data.name, type: 'date' });
      await customFieldsPage.verifyFieldExists(data.name);
      await customFieldsPage.verifyFieldType(data.name, FIELD_TYPE_LABELS.date);
    });

    test('should create a user field', async () => {
      const data = generateCustomField('user');
      createdFieldNames.push(data.name);

      await customFieldsPage.goto();
      await customFieldsPage.createField({ name: data.name, type: 'user' });
      await customFieldsPage.verifyFieldExists(data.name);
      await customFieldsPage.verifyFieldType(data.name, FIELD_TYPE_LABELS.user);
    });

    test('should create an iteration field', async () => {
      const data = generateCustomField('iteration');
      createdFieldNames.push(data.name);

      await customFieldsPage.goto();
      await customFieldsPage.createField({ name: data.name, type: 'iteration' });
      await customFieldsPage.verifyFieldExists(data.name);
      await customFieldsPage.verifyFieldType(data.name, FIELD_TYPE_LABELS.iteration);
    });

    test('should create a milestone field', async () => {
      const data = generateCustomField('milestone');
      createdFieldNames.push(data.name);

      await customFieldsPage.goto();
      await customFieldsPage.createField({ name: data.name, type: 'milestone' });
      await customFieldsPage.verifyFieldExists(data.name);
      await customFieldsPage.verifyFieldType(data.name, FIELD_TYPE_LABELS.milestone);
    });

    test('should create a portal customer field', async () => {
      const data = generateCustomField('portalcustomer');
      createdFieldNames.push(data.name);

      await customFieldsPage.goto();
      await customFieldsPage.createField({ name: data.name, type: 'portalcustomer' });
      await customFieldsPage.verifyFieldExists(data.name);
      await customFieldsPage.verifyFieldType(data.name, FIELD_TYPE_LABELS.portalcustomer);
    });

    test('should create a customer organisation field', async () => {
      const data = generateCustomField('customerorganisation');
      createdFieldNames.push(data.name);

      await customFieldsPage.goto();
      await customFieldsPage.createField({ name: data.name, type: 'customerorganisation' });
      await customFieldsPage.verifyFieldExists(data.name);
      await customFieldsPage.verifyFieldType(data.name, FIELD_TYPE_LABELS.customerorganisation);
    });
  });

  test.describe('Edit Custom Fields', () => {
    test('keeps the field type immutable after creation', async ({ page }) => {
      const data = generateCustomField('text', 'immutable-type');
      createdFieldNames.push(data.name);

      await customFieldsPage.goto();
      await customFieldsPage.createField({ name: data.name, type: 'text' });
      await customFieldsPage.openFieldForEdit(data.name);

      await expect(page.getByTestId('custom-field-type-trigger')).toBeDisabled();
    });
  });

  test.describe('Delete Custom Fields', () => {
    test('should delete a custom field', async ({ request }) => {
      // Create a field to delete
      const data = generateCustomField('text', 'delete-test');

      await customFieldsPage.goto();
      await customFieldsPage.createField({ name: data.name, type: 'text' });
      await customFieldsPage.verifyFieldExists(data.name);

      // Delete via UI
      await customFieldsPage.deleteField(data.name);
      await customFieldsPage.verifyFieldDoesNotExist(data.name);
    });
  });
});
