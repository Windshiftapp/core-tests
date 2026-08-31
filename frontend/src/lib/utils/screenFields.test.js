import { describe, expect, it } from 'vitest';
import {
  buildDetailScreenFieldConfig,
  canSystemFieldBeRequiredOnCreate,
  dedupeScreenFields,
  isCreateSystemFieldAutoManaged,
  isCreateSystemFieldRenderable,
  isSystemFieldAvailableForItem,
  isSystemFieldConfigured,
  normalizeSystemFieldIdentifier,
  resolveEffectiveScreenIds,
  splitScreenFields,
} from './screenFields.js';

describe('screenFields utilities', () => {
  it('resolves effective screen ids with create fallback per context', () => {
    const configSet = {
      create_screen_id: 10,
      edit_screen_id: null,
      view_screen_id: 30,
      item_type_configs: [
        {
          item_type_id: 5,
          create_screen_id: 11,
          edit_screen_id: null,
          view_screen_id: null,
        },
      ],
    };

    expect(resolveEffectiveScreenIds(configSet, 5)).toEqual({
      create: 11,
      edit: 11,
      view: 11,
    });
    expect(resolveEffectiveScreenIds(configSet, 7)).toEqual({
      create: 10,
      edit: 10,
      view: 30,
    });
  });

  it('uses fallback screen id only when no config screen resolves', () => {
    expect(resolveEffectiveScreenIds(null, 1, 99)).toEqual({ create: 99, edit: 99, view: 99 });
    expect(resolveEffectiveScreenIds({}, 1, 99)).toEqual({ create: 99, edit: 99, view: 99 });
  });

  it('resolves system-field availability per item type and workspace', () => {
    const configSetsByWorkspaceId = new Map([
      [
        '10',
        {
          edit_screen_id: 20,
          item_type_configs: [{ item_type_id: 2, edit_screen_id: 30 }],
        },
      ],
      ['11', null],
    ]);
    const screensById = new Map([
      [20, { fields: [{ field_type: 'system', field_identifier: 'priority' }] }],
      [30, { fields: [{ field_type: 'system', field_identifier: 'story_points' }] }],
      [1, { fields: [{ field_type: 'system', field_identifier: 'story_points' }] }],
    ]);

    expect(
      isSystemFieldAvailableForItem(
        { workspace_id: 10, item_type_id: 1 },
        'story_points',
        configSetsByWorkspaceId,
        screensById
      )
    ).toBe(false);
    expect(
      isSystemFieldAvailableForItem(
        { workspace_id: 10, item_type_id: 2 },
        'story_points',
        configSetsByWorkspaceId,
        screensById
      )
    ).toBe(true);
    expect(
      isSystemFieldAvailableForItem(
        { workspace_id: 11, item_type_id: 1 },
        'story_points',
        configSetsByWorkspaceId,
        screensById
      )
    ).toBe(true);
    expect(
      isSystemFieldAvailableForItem(
        { workspace_id: 12, item_type_id: 1 },
        'story_points',
        configSetsByWorkspaceId,
        screensById
      )
    ).toBe(false);
    expect(
      isSystemFieldAvailableForItem(
        { workspace_id: 10, item_type_id: 1 },
        'story_points',
        new Map([['10', undefined]]),
        screensById
      )
    ).toBe(false);
  });

  it('splits system and custom fields', () => {
    const fields = [
      { field_type: 'system', field_identifier: 'priority' },
      { field_type: 'custom', field_identifier: '42' },
      { field_type: 'default', field_identifier: 'legacy' },
    ];

    expect(splitScreenFields(fields)).toEqual({
      customFields: [{ field_type: 'custom', field_identifier: '42' }],
      systemFields: [{ field_type: 'system', field_identifier: 'priority' }],
      systemFieldIdentifiers: ['priority'],
    });
  });

  it('handles system field aliases', () => {
    expect(normalizeSystemFieldIdentifier('estimate_minutes')).toBe('estimate');
    expect(isSystemFieldConfigured(['estimate_minutes'], 'estimate')).toBe(true);
    expect(isSystemFieldConfigured(['estimate'], 'estimate_minutes')).toBe(true);
  });

  it('dedupes system aliases and field identifiers', () => {
    const fields = [
      { field_type: 'system', field_identifier: 'estimate_minutes' },
      { field_type: 'system', field_identifier: 'estimate' },
      { field_type: 'custom', field_identifier: '9' },
      { field_type: 'custom', field_identifier: '9' },
    ];

    expect(dedupeScreenFields(fields)).toEqual([
      { field_type: 'system', field_identifier: 'estimate_minutes' },
      { field_type: 'custom', field_identifier: '9' },
    ]);
  });

  it('classifies create renderable and auto-managed system fields', () => {
    expect(isCreateSystemFieldRenderable('iteration')).toBe(true);
    expect(isCreateSystemFieldRenderable('estimate_minutes')).toBe(true);
    expect(isCreateSystemFieldRenderable('labels')).toBe(true);
    expect(isCreateSystemFieldAutoManaged('status')).toBe(true);
    expect(canSystemFieldBeRequiredOnCreate('story_points')).toBe(true);
    expect(canSystemFieldBeRequiredOnCreate('labels')).toBe(true);
    expect(canSystemFieldBeRequiredOnCreate('status')).toBe(false);
  });

  it('keeps edit-screen fields visible and marks only edit fields editable in detail', () => {
    const editScreen = {
      id: 10,
      fields: [
        { id: 101, field_type: 'system', field_identifier: 'priority' },
        { id: 102, field_type: 'custom', field_identifier: '7' },
      ],
    };
    const viewScreen = {
      id: 20,
      fields: [
        { id: 201, field_type: 'system', field_identifier: 'assignee' },
        { id: 202, field_type: 'custom', field_identifier: '7' },
        { id: 203, field_type: 'custom', field_identifier: '8' },
      ],
    };

    const config = buildDetailScreenFieldConfig(editScreen, viewScreen);

    expect(config.visibleSystemFields).toEqual([
      'title',
      'description',
      'status',
      'priority',
      'assignee',
    ]);
    expect(config.visibleCustomFields).toEqual([
      { id: 102, field_type: 'custom', field_identifier: '7' },
      { id: 203, field_type: 'custom', field_identifier: '8' },
    ]);
    expect(config.editableSystemFields).toEqual(
      new Set(['title', 'description', 'status', 'priority'])
    );
    expect(config.editableCustomFieldIds).toEqual(new Set([7]));
  });

  it('keeps locked fields visible for custom-only detail screens', () => {
    const screen = {
      id: 10,
      fields: [{ id: 102, field_type: 'custom', field_identifier: '7' }],
    };

    expect(buildDetailScreenFieldConfig(screen, null).visibleSystemFields).toEqual([
      'title',
      'description',
      'status',
    ]);
  });

  it('treats a single detail screen as legacy all-visible editable mode', () => {
    const screen = {
      id: 10,
      fields: [
        { id: 101, field_type: 'system', field_identifier: 'priority' },
        { id: 102, field_type: 'custom', field_identifier: '7' },
      ],
    };

    expect(buildDetailScreenFieldConfig(screen, null)).toEqual({
      visibleCustomFields: [{ id: 102, field_type: 'custom', field_identifier: '7' }],
      visibleSystemFields: ['title', 'description', 'status', 'priority'],
      editableCustomFieldIds: null,
      editableSystemFields: null,
    });
  });
});
