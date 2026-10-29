import { describe, expect, it } from 'vitest';
import { buildDetailScreenFieldConfig, canSystemFieldBeRequiredOnCreate, dedupeScreenFields, isAlwaysVisibleSystemField, isCreateSystemFieldAutoManaged, isCreateSystemFieldRenderable, isSystemFieldAvailableInEffectiveConfig, isSystemFieldConfigured, normalizeSystemFieldIdentifier, splitScreenFields } from './screenFields.js';

describe('screenFields utilities', () => {
  it('resolves system-field availability from the effective config edit screen', () => {
    expect(
      isSystemFieldAvailableInEffectiveConfig(
        { edit_screen: { fields: [{ field_type: 'system', field_identifier: 'story_points' }] } },
        'story_points'
      )
    ).toBe(true);
    expect(
      isSystemFieldAvailableInEffectiveConfig(
        { edit_screen: { fields: [{ field_type: 'system', field_identifier: 'priority' }] } },
        'story_points'
      )
    ).toBe(false);
    expect(
      isSystemFieldAvailableInEffectiveConfig({ edit_screen: null }, 'story_points')
    ).toBe(false);
    expect(isSystemFieldAvailableInEffectiveConfig({}, 'story_points')).toBe(false);
    expect(isSystemFieldAvailableInEffectiveConfig(null, 'story_points')).toBe(false);
  });

  it('honors system field aliases from the effective config edit screen', () => {
    const config = {
      edit_screen: { fields: [{ field_type: 'system', field_identifier: 'estimate_minutes' }] },
    };
    expect(isSystemFieldAvailableInEffectiveConfig(config, 'estimate')).toBe(true);
    expect(isSystemFieldAvailableInEffectiveConfig(config, 'story_points')).toBe(false);
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

  it('treats team as a manually-added system field, not a default one', () => {
    expect(isCreateSystemFieldRenderable('team')).toBe(true);
    expect(isCreateSystemFieldAutoManaged('team')).toBe(false);
    expect(canSystemFieldBeRequiredOnCreate('team')).toBe(true);
    // Team must be added to a screen explicitly; it is not pinned to every item.
    expect(isAlwaysVisibleSystemField('team')).toBe(false);
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
