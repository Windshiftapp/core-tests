import { createCustomFieldViaAPI, deleteCustomFieldViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';

test('searches custom multiselect values in the QL builder', async ({ page, request }) => {
  const stamp = Date.now();
  const fieldName = `Approver groups ${stamp}`;
  const field = await createCustomFieldViaAPI(request, {
    name: fieldName,
    field_type: 'multiselect',
    options: JSON.stringify({
      next_id: 3,
      items: [
        { id: 1, label: 'Developers' },
        { id: 2, label: 'Operations approvers' },
      ],
    }),
  });
  const fieldID = field?.id ?? field?.data?.id;

  try {
    await page.goto('/search');
    await page.getByTestId('global-search-add-dynamic-filter').click();

    const filter = page.getByTestId('global-search-dynamic-filter-0');
    await filter.getByTestId('field-selector-trigger').click();
    await page.getByTestId(`field-option-cfid_${fieldID}`).click();

    const valueSearch = page.getByTestId('global-search-dynamic-filter-0-value-search');
    await valueSearch.click();
    await valueSearch.fill('operations');
    await page.getByTestId('global-search-dynamic-filter-0-value-option-2').click();

    await expect(page.getByTestId('ql-query-summary')).toHaveText(`\`cfid_${fieldID}\` = "2"`);
    await expect(valueSearch).toHaveValue('Operations approvers');
  } finally {
    if (fieldID) await deleteCustomFieldViaAPI(request, fieldID);
  }
});
