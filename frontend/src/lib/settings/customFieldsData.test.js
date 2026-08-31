import { describe, expect, it, vi } from 'vitest';
import { customFieldFormData, loadCustomFieldsOverview } from './customFieldsData.js';

describe('custom field editor form data', () => {
  it('preserves hidden metadata when editing an existing field', () => {
    expect(
      customFieldFormData({
        name: 'Customer impact',
        field_type: 'text',
        description: 'Shown to workspace administrators',
        required: true,
        applies_to_portal_customers: true,
      })
    ).toEqual({
      field_name: 'Customer impact',
      field_type: 'text',
      field_config: { max_length: '' },
      description: 'Shown to workspace administrators',
      required: true,
      applies_to_portal_customers: true,
      applies_to_customer_organisations: false,
    });
  });

  it('uses safe defaults when creating a field', () => {
    expect(customFieldFormData()).toEqual({
      field_name: '',
      field_type: 'text',
      field_config: { max_length: '' },
      description: '',
      required: false,
      applies_to_portal_customers: false,
      applies_to_customer_organisations: false,
    });
  });
});

describe('custom fields screen request graph', () => {
  it('loads every screen assignment with two bounded requests', async () => {
    const apiClient = {
      customFields: {
        getAll: vi.fn().mockResolvedValue({
          data: [{ id: 7 }],
          index_counts: { items: { current: 2, max: 20 }, assets: { current: 1, max: 20 } },
        }),
      },
      screens: {
        getAllWithFields: vi.fn().mockResolvedValue([{ id: 1, fields: [{ id: 10 }] }, { id: 2 }]),
        getFields: vi.fn(),
      },
    };

    const loading = loadCustomFieldsOverview(apiClient);

    expect(apiClient.customFields.getAll).toHaveBeenCalledOnce();
    expect(apiClient.screens.getAllWithFields).toHaveBeenCalledOnce();
    expect(apiClient.screens.getFields).not.toHaveBeenCalled();
    const overview = await loading;
    expect(overview.customFields).toEqual([{ id: 7 }]);
    expect(overview.indexCounts.items.current).toBe(2);
    expect(overview.screens).toEqual([
      { id: 1, fields: [{ id: 10 }] },
      { id: 2, fields: [] },
    ]);
  });

  it('preserves the custom field list when screen metadata fails to load', async () => {
    const apiClient = {
      customFields: {
        getAll: vi.fn().mockResolvedValue({
          data: [{ id: 7 }, { id: 8 }],
          index_counts: { items: { current: 0, max: 20 }, assets: { current: 0, max: 20 } },
        }),
      },
      screens: {
        getAllWithFields: vi.fn().mockRejectedValue(new Error('orphaned screen field')),
      },
    };

    const overview = await loadCustomFieldsOverview(apiClient);

    expect(overview.customFields).toEqual([{ id: 7 }, { id: 8 }]);
    expect(overview.screens).toEqual([]);
  });
});
