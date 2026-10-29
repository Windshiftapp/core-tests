import { beforeEach, describe, expect, it, vi } from 'vitest';
import { configurationSets, itemTemplates, screens } from '../api/configuration.js';
import { workspaces } from '../api/workspaces.js';



vi.mock('../api/configuration.js', () => ({
  screens: { getFields: vi.fn() },
  itemTemplates: { getAll: vi.fn() },
  configurationSets: { getAll: vi.fn(), get: vi.fn() },
}))

vi.mock('../api/workspaces.js', () => ({
  workspaces: {
        getTemplates: vi.fn(),
        getEffectiveConfig: vi.fn().mockResolvedValue({ screens: { create: 1 } }),
      },
}));



const { workItemFormStore } = await import('./workItemFormStore.svelte.js');
const { workspaceDataStore } = await import('./workspaceDataStore.svelte.js');

const ASSET_FIELD = { id: 3, name: 'Component', field_type: 'asset' };
const TEXT_FIELD = { id: 9, name: 'Notes', field_type: 'text' };
const MULTISELECT_FIELD = { id: 12, name: 'Regions', field_type: 'multiselect' };
const LINKING_FIELD = { id: 21, name: 'Blocks', field_type: 'linking' };

function screenFieldsFor(customFieldIds) {
  return [
    { field_type: 'system', field_identifier: 'title', is_required: true },
    ...customFieldIds.map((id) => ({
      field_type: 'custom',
      field_identifier: String(id),
      is_required: false,
    })),
  ];
}

describe('workItemFormStore custom field values across screen loads', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    workItemFormStore.reset();
    workItemFormStore.allCustomFields = [ASSET_FIELD, TEXT_FIELD, LINKING_FIELD];
    workItemFormStore.customFieldsLoaded = true;
    workItemFormStore.currentConfigSet = {
      create_screen_id: 5,
      edit_screen_id: 5,
      view_screen_id: 5,
    };
  });

  it('keeps entered values for fields that stay configured when the item type changes', async () => {
    screens.getFields.mockResolvedValue(screenFieldsFor([3]));

    await workItemFormStore.loadScreenFieldsForItemType(6, 1);
    expect(workItemFormStore.customFieldValues[3]).toBe('');

    const picked = { id: 42, title: 'Bearing', asset_tag: '' };
    workItemFormStore.customFieldValues[3] = picked;

    // Switching the item type reloads the screen fields for the new key.
    await workItemFormStore.loadScreenFieldsForItemType(6, 2);

    expect(workItemFormStore.customFieldValues[3]).toEqual(picked);
  });

  it('initializes only fields that are new to the screen', async () => {
    screens.getFields
      .mockResolvedValueOnce(screenFieldsFor([3]))
      .mockResolvedValueOnce(screenFieldsFor([3, 9]));

    await workItemFormStore.loadScreenFieldsForItemType(6, 1);
    workItemFormStore.customFieldValues[3] = { id: 7, title: 'Gear', asset_tag: '' };

    await workItemFormStore.loadScreenFieldsForItemType(6, 2);

    expect(workItemFormStore.customFieldValues[3]).toEqual({ id: 7, title: 'Gear', asset_tag: '' });
    expect(workItemFormStore.customFieldValues[9]).toBe('');
  });

  it('initializes an untouched multiselect as an array, not a string', async () => {
    workItemFormStore.allCustomFields = [MULTISELECT_FIELD];
    screens.getFields.mockResolvedValue(screenFieldsFor([12]));

    await workItemFormStore.loadScreenFieldsForItemType(6, 1);

    expect(workItemFormStore.customFieldValues[12]).toEqual([]);
    expect(Array.isArray(workItemFormStore.getFormData().custom_field_values[12])).toBe(true);
  });

  it('resetForm keeps a defined value for every rendered custom field', () => {
    workItemFormStore.customFields = [MULTISELECT_FIELD, TEXT_FIELD];
    // A close clears the map while customFields still holds the old screen.
    workItemFormStore.customFieldValues = {};

    workItemFormStore.resetForm();

    expect(workItemFormStore.customFieldValues[12]).toEqual([]);
    expect(workItemFormStore.customFieldValues[9]).toBe('');
  });

  it('excludes linking custom fields from the create screen and payload', async () => {
    screens.getFields.mockResolvedValue(screenFieldsFor([3, 21]));

    await workItemFormStore.loadScreenFieldsForItemType(6, 1);

    expect(workItemFormStore.customFields.map((field) => field.id)).toEqual([3]);
    expect(workItemFormStore.customFieldValues[21]).toBeUndefined();
    expect(workItemFormStore.getFormData().custom_field_values).not.toHaveProperty('21');
  });

  it('drops values for fields no longer configured on the new screen', async () => {
    screens.getFields
      .mockResolvedValueOnce(screenFieldsFor([3]))
      .mockResolvedValueOnce(screenFieldsFor([9]));

    await workItemFormStore.loadScreenFieldsForItemType(6, 1);
    workItemFormStore.customFieldValues[3] = { id: 7, title: 'Gear', asset_tag: '' };

    await workItemFormStore.loadScreenFieldsForItemType(6, 2);

    expect(workItemFormStore.customFieldValues[3]).toBeUndefined();
    expect(workItemFormStore.customFieldValues[9]).toBe('');
  });
});

describe('workItemFormStore workspace scope', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    workItemFormStore.reset();
    workspaceDataStore.reset();
  });

  it('resolves a non-active workspace config without activating the shared scope', async () => {
    const initialize = vi.spyOn(workspaceDataStore, 'initialize');
    workspaces.getEffectiveConfig.mockResolvedValue({
      priorities: [{ id: 1, name: 'High', sort_order: 1 }],
      item_type_ids: [7],
      default_item_type_id: 7,
    });

    await workItemFormStore.loadConfigSetForWorkspace(6);

    expect(workspaces.getEffectiveConfig).toHaveBeenCalledWith(6, null, expect.anything());
    expect(initialize).not.toHaveBeenCalled();
    expect(workspaceDataStore.workspaceId).toBeNull();
    expect(workItemFormStore.configSetLoadedForWorkspace).toBe(6);
    expect(workItemFormStore.currentConfigSet).toMatchObject({ default_item_type_id: 7 });
  });

  it('uses the shared cache when the selected workspace is active', async () => {
    workspaceDataStore.workspaceId = 6;
    const initialize = vi.spyOn(workspaceDataStore, 'initialize');
    const screenConfig = vi.spyOn(workspaceDataStore, 'screenConfig');
    workspaces.getEffectiveConfig.mockResolvedValue({
      priorities: [],
      item_type_ids: [],
      default_item_type_id: null,
    });

    await workItemFormStore.loadConfigSetForWorkspace(6);

    expect(screenConfig).toHaveBeenCalledWith(null);
    expect(initialize).not.toHaveBeenCalled();
  });
});
