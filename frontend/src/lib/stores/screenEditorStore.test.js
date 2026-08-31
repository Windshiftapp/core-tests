import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api.js', () => ({ api: {} }));

const { api } = await import('../api.js');
const { screenEditorStore } = await import('./screenEditorStore.svelte.js');

describe('screenEditorStore required-field constraints', () => {
  beforeEach(() => {
    screenEditorStore.reset();
    api.screens = {
      getFields: vi.fn().mockResolvedValue([]),
      updateFields: vi.fn().mockResolvedValue([]),
    };
    api.customFields = {
      getAll: vi.fn().mockResolvedValue({ data: [] }),
    };
  });

  it('prevents enabling required for system fields create cannot satisfy', () => {
    screenEditorStore.screenFields = [
      { field_type: 'system', field_identifier: 'status', is_required: false },
    ];

    screenEditorStore.toggleFieldRequired(0);

    expect(screenEditorStore.screenFields[0].is_required).toBe(false);
  });

  it('allows clearing legacy invalid required flags', () => {
    screenEditorStore.screenFields = [
      { field_type: 'system', field_identifier: 'status', is_required: true },
    ];

    screenEditorStore.toggleFieldRequired(0);

    expect(screenEditorStore.screenFields[0].is_required).toBe(false);
  });

  it('allows required custom fields and renderable system fields', () => {
    screenEditorStore.screenFields = [
      { field_type: 'custom', field_identifier: '7', is_required: false },
      { field_type: 'system', field_identifier: 'story_points', is_required: false },
    ];

    screenEditorStore.toggleFieldRequired(0);
    screenEditorStore.toggleFieldRequired(1);

    expect(screenEditorStore.screenFields.map((field) => field.is_required)).toEqual([true, true]);
  });

  it('auto-adds always-visible title, description, and status fields', async () => {
    api.screens.getFields.mockResolvedValue([
      { field_type: 'system', field_identifier: 'title', display_order: 0, is_required: true },
      { field_type: 'system', field_identifier: 'status', display_order: 1, is_required: false },
    ]);

    await screenEditorStore.startEditFields({ id: 12 });

    expect(screenEditorStore.screenFields.map((field) => field.field_identifier)).toEqual([
      'title',
      'description',
      'status',
    ]);
  });

  it('normalizes locked field required flags so status stays auto-managed on create', async () => {
    api.screens.getFields.mockResolvedValue([
      { field_type: 'system', field_identifier: 'title', display_order: 0, is_required: false },
      {
        field_type: 'system',
        field_identifier: 'description',
        display_order: 1,
        is_required: true,
      },
      { field_type: 'system', field_identifier: 'status', display_order: 2, is_required: true },
    ]);

    await screenEditorStore.startEditFields({ id: 12 });

    expect(screenEditorStore.screenFields.map((field) => field.is_required)).toEqual([
      true,
      false,
      false,
    ]);
  });

  it('prevents removing always-visible system fields', () => {
    screenEditorStore.screenFields = [
      { field_type: 'system', field_identifier: 'description', display_order: 0 },
      { field_type: 'system', field_identifier: 'priority', display_order: 1 },
    ];

    screenEditorStore.removeField(0);
    expect(screenEditorStore.screenFields.map((field) => field.field_identifier)).toEqual([
      'description',
      'priority',
    ]);

    screenEditorStore.removeField(1);
    expect(screenEditorStore.screenFields.map((field) => field.field_identifier)).toEqual([
      'description',
    ]);
  });

  it('excludes always-visible fields from the available field list', () => {
    const identifiers = screenEditorStore.availableFieldsFiltered
      .filter((field) => field.type === 'system')
      .map((field) => field.identifier);

    expect(identifiers).not.toContain('title');
    expect(identifiers).not.toContain('description');
    expect(identifiers).not.toContain('status');
  });
});
