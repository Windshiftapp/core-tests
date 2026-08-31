import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const dragMocks = vi.hoisted(() => ({
  draggables: [],
  targets: [],
}));

const apiMocks = vi.hoisted(() => ({
  getFields: vi.fn(),
  getAvailableFields: vi.fn(),
  get: vi.fn(),
  updateFields: vi.fn(),
  update: vi.fn(),
}));

vi.mock('@atlaskit/pragmatic-drag-and-drop/element/adapter', () => ({
  draggable: vi.fn((config) => {
    dragMocks.draggables.push(config);
    return vi.fn();
  }),
  dropTargetForElements: vi.fn((config) => {
    dragMocks.targets.push(config);
    return vi.fn();
  }),
}));

vi.mock('@atlaskit/pragmatic-drag-and-drop-hitbox/closest-edge', () => ({
  attachClosestEdge: vi.fn((_data, { allowedEdges }) => ({ edge: allowedEdges[0] })),
  extractClosestEdge: vi.fn((data) => data.edge),
}));

vi.mock('../api.js', () => ({
  api: {
    requestTypes: {
      getFields: apiMocks.getFields,
      getAvailableFields: apiMocks.getAvailableFields,
      get: apiMocks.get,
      updateFields: apiMocks.updateFields,
      update: apiMocks.update,
    },
  },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

import RequestTypeFieldsBuilder from './RequestTypeFieldsBuilder.svelte';

const baseFields = [
  {
    field_identifier: 'title',
    field_type: 'default',
    display_order: 0,
    is_required: true,
    field_name: 'Title',
    step_number: 1,
  },
  {
    field_identifier: 'description',
    field_type: 'default',
    display_order: 1,
    is_required: false,
    field_name: 'Description',
    step_number: 1,
  },
];

function renderBuilder(fields) {
  apiMocks.getFields.mockResolvedValue(structuredClone(fields));
  apiMocks.getAvailableFields.mockResolvedValue([]);
  apiMocks.get.mockResolvedValue({
    id: 9,
    name: 'Support',
    item_type_id: 3,
    icon: 'FileText',
    color: '#2563eb',
    is_active: true,
  });
  apiMocks.updateFields.mockResolvedValue([]);
  apiMocks.update.mockResolvedValue({});

  return render(RequestTypeFieldsBuilder, {
    requestTypeId: 9,
    requestTypeName: 'Support',
    channelId: 4,
  });
}

function latestTarget(predicate) {
  return [...dragMocks.targets].reverse().find((target) => predicate(target.element));
}

describe('RequestTypeFieldsBuilder', () => {
  beforeEach(() => {
    dragMocks.draggables.length = 0;
    dragMocks.targets.length = 0;
  });

  it('reorders fields by stable identifiers and moves a field to another step', async () => {
    renderBuilder(baseFields);

    await screen.findByTestId('request-type-field-drag-title');
    await waitFor(() => expect(dragMocks.targets.length).toBeGreaterThan(0));

    const titleTarget = latestTarget((element) => element.dataset.fieldId === 'title');
    titleTarget.onDrop({
      self: { data: { edge: 'top' } },
      source: { data: { type: 'configured-field', fieldId: 'description', sourceStep: 1 } },
    });

    await waitFor(() => {
      const saved = apiMocks.updateFields.mock.calls.at(-1)[2];
      expect(saved.map((field) => field.field_identifier)).toEqual(['title', 'description']);
      expect(saved.find((field) => field.field_identifier === 'description').display_order).toBe(0);
      expect(saved.find((field) => field.field_identifier === 'title').display_order).toBe(1);
    });

    await fireEvent.click(screen.getByTitle('requestTypeFields.addNewStep'));
    await fireEvent.click(screen.getByTestId('request-type-step-1'));
    await waitFor(() => expect(screen.getByTestId('request-type-step-2')).toBeInTheDocument());

    const stepTwoTarget = latestTarget((element) => element.dataset.stepDropTarget === '2');
    stepTwoTarget.onDrop({
      source: { data: { type: 'configured-field', fieldId: 'title', sourceStep: 1 } },
    });

    await waitFor(() => {
      const saved = apiMocks.updateFields.mock.calls.at(-1)[2];
      expect(saved.find((field) => field.field_identifier === 'title').step_number).toBe(2);
    });
  });

  it('edits and persists options on an existing virtual select field', async () => {
    renderBuilder([
      {
        field_identifier: 'vf_priority',
        field_type: 'virtual',
        virtual_field_type: 'select',
        virtual_field_options: JSON.stringify([{ value: 'normal', label: 'Normal' }]),
        display_order: 0,
        is_required: false,
        display_name: 'Priority',
        field_name: 'Priority',
        step_number: 1,
      },
    ]);

    await fireEvent.click(await screen.findByTestId('request-type-field-edit-vf_priority'));
    const labelInput = await screen.findByTestId('request-type-edit-option-label-0');
    await fireEvent.input(labelInput, { target: { value: 'Standard' } });
    await fireEvent.click(screen.getByRole('button', { name: 'common.save' }));

    await waitFor(() => {
      const saved = apiMocks.updateFields.mock.calls.at(-1)[2][0];
      expect(JSON.parse(saved.virtual_field_options)).toEqual([
        { value: 'normal', label: 'Standard' },
      ]);
    });
  });
});
