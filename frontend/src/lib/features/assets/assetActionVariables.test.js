import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api/assets.js', () => ({
  assetTypes: { getFields: vi.fn() },
}));

import { assetTypes } from '../../api/assets.js';
import { assetActionConditionFields, loadAssetActionCustomFields } from './assetActionVariables.js';

describe('asset action condition variables', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('offers the executor canonical field names', () => {
    expect(assetActionConditionFields.map(({ value }) => value)).toEqual([
      'asset_title',
      'asset_tag',
      'asset_type_name',
      'asset_status_name',
    ]);
  });

  it('loads only fields assigned to the selected asset type', async () => {
    assetTypes.getFields.mockResolvedValue([
      {
        custom_field_id: 17,
        field_name: 'Serial number',
        field_type: 'text',
        field_description: 'Hardware serial',
      },
    ]);

    await expect(loadAssetActionCustomFields(null)).resolves.toEqual([]);
    expect(assetTypes.getFields).not.toHaveBeenCalled();

    await expect(loadAssetActionCustomFields(9)).resolves.toEqual([
      {
        id: '17',
        name: 'Serial number',
        type: 'text',
        description: 'Hardware serial',
        isCustom: true,
      },
    ]);
    expect(assetTypes.getFields).toHaveBeenCalledWith(9);
  });
});
