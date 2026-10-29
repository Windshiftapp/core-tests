import { beforeEach, describe, expect, it, vi } from 'vitest';
import { itemTemplates } from '../api/configuration.js';

const { itemTemplatesGetAll } = vi.hoisted(() => ({
  itemTemplatesGetAll: vi.fn(),
}));

vi.mock('../api/configuration.js', () => ({
  itemTemplates: { getAll: itemTemplatesGetAll },
}));

const { workItemFormStore } = await import('./workItemFormStore.svelte.js');

describe('workItemFormStore screen-field validation', () => {
  beforeEach(() => {
    workItemFormStore.reset();
    workItemFormStore.selectedWorkspace = { id: 2 };
    workItemFormStore.formData.workspace_id = 2;
    workItemFormStore.formData.name = 'Test item';
    workItemFormStore.formData.item_type_id = 5;
    itemTemplatesGetAll.mockReset();
    itemTemplatesGetAll.mockResolvedValue([]);
  });

  it('defers template loading until the workspace config set is loaded', async () => {
    workItemFormStore.configSetLoadedForWorkspace = null;

    await workItemFormStore.loadTemplatesForCurrentType();
    expect(itemTemplatesGetAll).not.toHaveBeenCalled();

    workItemFormStore.configSetLoadedForWorkspace = 2;
    await workItemFormStore.loadTemplatesForCurrentType();
    expect(itemTemplatesGetAll).toHaveBeenCalledWith(2, { item_type_id: 5 });
  });

  it('validates required milestone against milestone_ids', () => {
    workItemFormStore.screenFields = [
      {
        field_type: 'system',
        field_identifier: 'milestone',
        is_required: true,
      },
    ];

    expect(workItemFormStore.validate()).toBe(false);
    expect(workItemFormStore.validationErrors).toEqual(['Milestone is required']);

    workItemFormStore.formData.milestone_ids = [10];

    expect(workItemFormStore.validate()).toBe(true);
  });

  it('resolves selected milestones when bound IDs are strings', () => {
    workItemFormStore.milestones = [
      { id: 10, name: '0.8.4' },
      { id: 11, name: '0.8.5' },
    ];
    workItemFormStore.formData.milestone_ids = ['10'];

    expect(workItemFormStore.selectedMilestones).toEqual([{ id: 10, name: '0.8.4' }]);
  });

  it('does not block create for auto-managed required system fields', () => {
    workItemFormStore.screenFields = [
      { field_type: 'system', field_identifier: 'status', is_required: true },
      {
        field_type: 'system',
        field_identifier: 'created_at',
        is_required: true,
      },
    ];

    expect(workItemFormStore.validate()).toBe(true);
  });

  it('validates required labels and includes them in the initial create payload', () => {
    workItemFormStore.screenFields = [
      { field_type: 'system', field_identifier: 'labels', is_required: true },
    ];

    expect(workItemFormStore.validate()).toBe(false);
    expect(workItemFormStore.validationErrors).toEqual(['Labels is required']);

    workItemFormStore.selectedLabels = [{ id: 5, name: 'UI' }];

    expect(workItemFormStore.validate()).toBe(true);
    expect(workItemFormStore.getFormData().label_ids).toEqual([5]);
  });

  it('treats an empty multiselect array as missing for a required field', () => {
    workItemFormStore.allCustomFields = [
      { id: 42, name: 'Regions', field_type: 'multiselect' },
    ];
    workItemFormStore.screenFields = [
      { field_type: 'custom', field_identifier: '42', is_required: true },
    ];
    workItemFormStore.customFieldValues = { 42: [] };

    expect(workItemFormStore.validate()).toBe(false);
    expect(workItemFormStore.validationErrors).toEqual(['Regions is required']);

    workItemFormStore.customFieldValues = { 42: [7] };

    expect(workItemFormStore.validate()).toBe(true);
  });

  it('does not block create for a required linking custom field', () => {
    workItemFormStore.allCustomFields = [
      { id: 77, name: 'Blocks', field_type: 'linking' },
    ];
    workItemFormStore.screenFields = [
      { field_type: 'custom', field_identifier: '77', is_required: true },
    ];
    workItemFormStore.customFieldValues = { 77: '' };

    expect(workItemFormStore.validate()).toBe(true);
    expect(workItemFormStore.validationErrors).toEqual([]);
  });

  it('submits newly renderable system fields in the create payload', () => {
    workItemFormStore.formData.iteration_id = 12;
    workItemFormStore.formData.project_id = 34;
    workItemFormStore.formData.story_points = '5.5';
    workItemFormStore.formData.estimate = '1d 2h 30m';
    workItemFormStore.formData.due_date = '2030-06-15';
    workItemFormStore.formData.start_date = '2030-06-01';
    workItemFormStore.formData.end_date = '2030-06-30';

    expect(workItemFormStore.getFormData()).toMatchObject({
      iteration_id: 12,
      project_id: 34,
      story_points: 5.5,
      estimate_minutes: 630,
      due_date: '2030-06-15T00:00:00.000Z',
      start_date: '2030-06-01T00:00:00.000Z',
      end_date: '2030-06-30T00:00:00.000Z',
    });
  });
});

describe('workItemFormStore defaults factory', () => {
  it('initializes from the same defaults factory that resetForm uses', () => {
    workItemFormStore.reset();
    workItemFormStore.availableItemTypes = [];
    const initial = JSON.parse(JSON.stringify(workItemFormStore.formData));

    workItemFormStore.formData.workspace_id = 1;
    workItemFormStore.formData.name = 'Changed';
    workItemFormStore.formData.label_names.push('UI');

    workItemFormStore.resetForm();

    expect(workItemFormStore.formData).toEqual(initial);
  });

  it('resetForm picks the first available item type as the default', () => {
    workItemFormStore.availableItemTypes = [{ id: 4 }, { id: 7 }];
    workItemFormStore.resetForm();

    expect(workItemFormStore.formData.item_type_id).toBe(4);
  });

  it('keeps the initial item type unconditional when no types are loaded', () => {
    workItemFormStore.availableItemTypes = [];
    workItemFormStore.resetForm();

    expect(workItemFormStore.formData.item_type_id).toBeNull();
  });
});
