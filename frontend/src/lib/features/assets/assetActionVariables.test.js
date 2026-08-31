import { describe, expect, it, vi } from 'vitest';
import { assetActionConditionFields, loadAssetActionCustomFields } from './assetActionVariables.js';

describe('asset action condition variables', () => {
  it('offers the executor canonical field names', () => {
    expect(assetActionConditionFields.map(({ value }) => value)).toEqual([
      'asset_title',
      'asset_tag',
      'asset_type_name',
      'asset_status_name',
    ]);
  });

  it('loads only fields assigned to the selected asset type', async () => {
    const getFields = vi.fn().mockResolvedValue([
      {
        custom_field_id: 17,
        field_name: 'Serial number',
        field_type: 'text',
        field_description: 'Hardware serial',
      },
    ]);
    const apiClient = { assetTypes: { getFields } };

    await expect(loadAssetActionCustomFields(apiClient, null)).resolves.toEqual([]);
    expect(getFields).not.toHaveBeenCalled();

    await expect(loadAssetActionCustomFields(apiClient, 9)).resolves.toEqual([
      {
        id: '17',
        name: 'Serial number',
        type: 'text',
        description: 'Hardware serial',
        isCustom: true,
      },
    ]);
    expect(getFields).toHaveBeenCalledWith(9);
  });
});
