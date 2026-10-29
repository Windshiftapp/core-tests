import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api/collections.js', () => ({
  collections: { getBoardConfigurationBootstrap: vi.fn(), get: vi.fn(), getBoardConfiguration: vi.fn() },
  collectionCategories: { getAll: vi.fn().mockResolvedValue([]) },
}));

import { collections } from '../api/collections.js';
import { loadBoardConfigurationPageData } from './boardConfigurationData.js';

describe('Board Configuration request graph', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('uses one route bootstrap and reuses shell reference data', async () => {
    collections.getBoardConfigurationBootstrap.mockResolvedValue({
      collection: { id: 9, name: 'Delivery', is_public: true, public_slug: 'delivery' },
      board_configuration: { id: 12, columns: [] },
      statuses: [{ id: 30, name: 'Open' }],
    });
    const referenceStore = {
      workspace: { id: 4, name: 'Workspace' },
      statuses: [{ id: 99 }],
      customFieldDefinitions: [{ id: 40, name: 'Impact' }],
      initialize: vi.fn().mockResolvedValue(undefined),
      initializeGlobal: vi.fn(),
    };

    const data = await loadBoardConfigurationPageData(referenceStore, 4, 9);

    expect(collections.getBoardConfigurationBootstrap).toHaveBeenCalledOnce();
    expect(collections.getBoardConfigurationBootstrap).toHaveBeenCalledWith(9, 4);
    expect(referenceStore.initialize).toHaveBeenCalledWith(4);
    expect(referenceStore.initializeGlobal).not.toHaveBeenCalled();
    expect(collections.get).not.toHaveBeenCalled();
    expect(collections.getBoardConfiguration).not.toHaveBeenCalled();
    expect(data).toEqual({
      workspace: { id: 4, name: 'Workspace' },
      collection: { id: 9, name: 'Delivery', is_public: true, public_slug: 'delivery' },
      boardConfiguration: { id: 12, columns: [] },
      statuses: [{ id: 30, name: 'Open' }],
      customFieldDefinitions: [{ id: 40, name: 'Impact' }],
    });
  });

  it('hides custom field layout references left by older deletions', async () => {
    collections.getBoardConfigurationBootstrap.mockResolvedValue({
      board_configuration: {
        id: 12,
        list_columns: [
          { field_type: 'system', field_identifier: 'title' },
          { field_type: 'custom', field_identifier: '40' },
          { field_type: 'custom', field_identifier: '41' },
        ],
        card_fields: [
          { field_type: 'system', field_identifier: 'status' },
          { field_type: 'custom', field_identifier: 'custom_field_40' },
          { field_type: 'custom', field_identifier: 'custom_field_41' },
        ],
        roadmap_config: {
          start_field_id: 'cf_41',
          end_field_id: 'cf_40',
          dependency_link_type_id: 5,
        },
      },
    });
    const referenceStore = {
      customFieldDefinitions: [{ id: 40, name: 'Impact' }],
      initialize: vi.fn().mockResolvedValue(undefined),
    };

    const data = await loadBoardConfigurationPageData(referenceStore, 4, 9);

    expect(data.boardConfiguration.list_columns.map((field) => field.field_identifier)).toEqual([
      'title',
      '40',
    ]);
    expect(data.boardConfiguration.card_fields.map((field) => field.field_identifier)).toEqual([
      'status',
      'custom_field_40',
    ]);
    expect(data.boardConfiguration.roadmap_config).toEqual({
      start_field_id: '',
      end_field_id: 'cf_40',
      dependency_link_type_id: 5,
    });
  });
});
