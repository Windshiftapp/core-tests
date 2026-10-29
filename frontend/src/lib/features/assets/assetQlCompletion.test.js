import { describe, expect, test } from 'vitest';
import { applyQlSuggestion, buildQlSuggestions, getQlCompletionContext } from '../../utils/qlCompletion.js';
import { buildAssetQlCatalog } from './assetQlCompletion.js';

describe('asset QL completion', () => {
  test('offers asset fields and nested categories without item fields', () => {
    const catalog = buildAssetQlCatalog({
      categories: [{ name: 'Hardware', children: [{ name: 'Laptops' }] }],
    });
    expect(catalog.fields.find((field) => field.name === 'category').values).toEqual([
      { value: 'Hardware', label: 'Hardware' },
      { value: 'Laptops', label: 'Laptops' },
    ]);
    const suggestions = buildQlSuggestions(getQlCompletionContext('asset_', 6, catalog), catalog);
    expect(suggestions.map((suggestion) => suggestion.value)).toContain('asset_tag');
    expect(catalog.fields.map((field) => field.name)).not.toContain('milestone');
  });

  test('inserts custom option IDs using stable field identifiers', () => {
    const catalog = buildAssetQlCatalog({
      customFields: [
        {
          custom_field_id: 7,
          field_name: 'Support tier',
          field_type: 'select',
          options: JSON.stringify({ items: [{ id: 'gold', label: 'Gold support' }] }),
        },
      ],
    });
    const field = catalog.fields.find((entry) => entry.name === 'cfid_7');
    expect(field.label).toBe('Support tier');
    const query = 'cfid_7 = ';
    const context = getQlCompletionContext(query, query.length, catalog);
    const suggestions = buildQlSuggestions(context, catalog, field.values);
    expect(applyQlSuggestion(query, context, suggestions[0]).query).toBe('cfid_7 = "gold"');
  });

  test('supports boolean values and tolerates malformed options', () => {
    const catalog = buildAssetQlCatalog({
      customFields: [
        { custom_field_id: 1, field_name: 'Enabled', field_type: 'checkbox' },
        { custom_field_id: 2, field_name: 'Tier', field_type: 'select', options: '{' },
      ],
    });
    expect(catalog.fields.find((field) => field.name === 'cfid_1').values).toEqual([
      { value: true, label: 'true' },
      { value: false, label: 'false' },
    ]);
    expect(catalog.fields.find((field) => field.name === 'cfid_2').values).toEqual([]);
    expect(buildAssetQlCatalog({}).fields.find((field) => field.name === 'status').values).toEqual(
      []
    );
  });
});
