import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api/configuration.js', () => ({
  customFields: { getOverview: vi.fn() },
  screens: { getAllWithFields: vi.fn(), getFields: vi.fn() },
}));

import { customFields, screens } from '../api/configuration.js';
import { customFieldFormData, customFieldUpdatePayload, linkingFieldOptions, loadCustomFieldsOverview } from './customFieldsData.js';

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
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('loads every screen assignment with two bounded requests', async () => {
    customFields.getOverview.mockResolvedValue({
      customFields: [{ id: 7 }],
      indexCounts: { items: { current: 2, max: 20 }, assets: { current: 1, max: 20 } },
    });
    screens.getAllWithFields.mockResolvedValue([{ id: 1, fields: [{ id: 10 }] }, { id: 2 }]);

    const loading = loadCustomFieldsOverview();

    expect(customFields.getOverview).toHaveBeenCalledOnce();
    expect(screens.getAllWithFields).toHaveBeenCalledOnce();
    expect(screens.getFields).not.toHaveBeenCalled();
    const overview = await loading;
    expect(overview.customFields).toEqual([{ id: 7 }]);
    expect(overview.indexCounts.items.current).toBe(2);
    expect(overview.screens).toEqual([
      { id: 1, fields: [{ id: 10 }] },
      { id: 2, fields: [] },
    ]);
  });

  it('preserves the custom field list when screen metadata fails to load', async () => {
    customFields.getOverview.mockResolvedValue({
      customFields: [{ id: 7 }, { id: 8 }],
      indexCounts: { items: { current: 0, max: 20 }, assets: { current: 0, max: 20 } },
    });
    screens.getAllWithFields.mockRejectedValue(new Error('orphaned screen field'));

    const overview = await loadCustomFieldsOverview();

    expect(overview.customFields).toEqual([{ id: 7 }, { id: 8 }]);
    expect(overview.screens).toEqual([]);
  });
});

describe('linkingFieldOptions', () => {
  it('passes mirror options through untouched so mirror edits keep their linkage', () => {
    const stored = {
      mirror_of_field_id: 12,
      link_type_id: 3,
      allowed_entity_types: ['item'],
      multi: false,
    };
    expect(
      linkingFieldOptions({
        editingOptions: stored,
        linkTypeId: null,
        mirrorName: 'Renamed mirror',
      })
    ).toEqual(stored);
  });

  it('builds full options for a primary linking field', () => {
    expect(
      linkingFieldOptions({
        linkTypeId: '5',
        allowedItemTypeIds: [1, 2],
        allowedEntityTypes: ['item', 'asset'],
        multi: false,
        mirrorName: ' Blocks ',
        mirrorAllowedItemTypeIds: [7],
      })
    ).toEqual({
      link_type_id: 5,
      allowed_entity_types: ['item', 'asset'],
      multi: false,
      allowed_item_type_ids: [1, 2],
      mirror_name: 'Blocks',
      mirror_allowed_item_type_ids: [7],
    });
  });

  it('omits optional keys when unset', () => {
    expect(
      linkingFieldOptions({ linkTypeId: '2' })
    ).toEqual({
      link_type_id: 2,
      allowed_entity_types: ['item'],
      multi: true,
    });
  });
});

describe('linkingFieldOptions primary mirror preservation', () => {
  it('carries mirror_field_id through a primary-field edit (WI-1166)', () => {
    const stored = {
      link_type_id: 3,
      allowed_entity_types: ['item'],
      multi: true,
      mirror_field_id: 44,
    };
    const built = linkingFieldOptions({
      editingOptions: stored,
      linkTypeId: '3',
      allowedEntityTypes: ['item'],
      multi: true,
      mirrorName: '',
    });
    expect(built.mirror_field_id).toBe(44);
    expect(built.link_type_id).toBe(3);
  });

  it('omits mirror_field_id when the field has no mirror', () => {
    expect(
      linkingFieldOptions({ linkTypeId: '2', editingOptions: null }).mirror_field_id
    ).toBeUndefined();
  });
});

describe('customFieldUpdatePayload', () => {
  it('carries display_order through edits instead of resetting it (WI-1169)', () => {
    const payload = customFieldUpdatePayload(
      { field_name: 'Severity', field_type: 'select', description: '', required: false },
      { id: 9, display_order: 4 },
    );
    expect(payload.display_order).toBe(4);
  });

  it('omits display_order on create', () => {
    const payload = customFieldUpdatePayload(
      { field_name: 'Severity', field_type: 'select', description: '', required: false },
      null,
    );
    expect(payload).not.toHaveProperty('display_order');
  });
});
