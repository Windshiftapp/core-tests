import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('../../api/transitions.js', () => ({
  transitions: { governance: vi.fn(), setScreen: vi.fn() },
}));

vi.mock('../../api/configuration.js', () => ({
  screens: {
    getAll: vi.fn(),
    getAllWithFields: vi.fn(),
    create: vi.fn(),
    getFields: vi.fn(),
    updateFields: vi.fn(),
  },
  customFields: { getAll: vi.fn() },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({ t: (key) => key }));
vi.mock('../../stores/toasts.svelte.js', () => ({
  surfaceError: vi.fn(),
  errorToast: vi.fn(),
  successToast: vi.fn(),
}));

import { customFields, screens } from '../../api/configuration.js';
import { transitions } from '../../api/transitions.js';
import { screenEditorStore } from '../../stores/screenEditorStore.svelte.js';
import TransitionInspector from './TransitionInspector.svelte';

const STATUSES = [
  { id: 1, name: 'Review' },
  { id: 2, name: 'Done' },
];

const BOUND_SCREEN = {
  id: 55,
  name: 'Resolution',
  field_count: 2,
  required_field_count: 1,
};

beforeEach(() => {
  vi.clearAllMocks();
  screenEditorStore.reset();
  screens.getAllWithFields.mockResolvedValue([BOUND_SCREEN]);
  screens.getFields.mockResolvedValue([]);
  screens.updateFields.mockResolvedValue([]);
  customFields.getAll.mockResolvedValue([]);
  transitions.setScreen.mockResolvedValue({ screen: null });
});

function renderInspector(transition) {
  return render(TransitionInspector, {
    props: { transition, statuses: STATUSES, onClose: vi.fn(), onChanged: vi.fn() },
  });
}

describe('TransitionInspector field editor', () => {
  test('opens the layout editor for the bound screen', async () => {
    transitions.governance.mockResolvedValue({ screen: BOUND_SCREEN });
    renderInspector({ transitionId: 101, edgeId: 'edge-1-2', from_status_id: 1, to_status_id: 2 });

    await screen.findByTestId('transition-inspector-screen-name');
    await fireEvent.click(screen.getByTestId('transition-inspector-edit-fields'));

    await waitFor(() => expect(screens.getFields).toHaveBeenCalledWith(BOUND_SCREEN.id));
    expect(await screen.findByTestId('screen-field-layout-editor')).toBeInTheDocument();
  });

  test('opens the layout editor after creating and binding a new screen', async () => {
    transitions.governance.mockResolvedValue({ screen: null });
    screens.create.mockResolvedValue({ id: 66, name: 'Escalation' });
    renderInspector({ transitionId: 101, edgeId: 'edge-1-2', from_status_id: 1, to_status_id: 2 });

    await screen.findByTestId('transition-inspector-no-screen');
    await fireEvent.input(screen.getByTestId('transition-inspector-new-screen-name'), {
      target: { value: 'Escalation' },
    });
    await fireEvent.click(screen.getByTestId('transition-inspector-create-screen'));

    await waitFor(() => expect(screens.create).toHaveBeenCalledWith({ name: 'Escalation' }));
    await waitFor(() => expect(transitions.setScreen).toHaveBeenCalledWith(101, 66));
    await waitFor(() => expect(screens.getFields).toHaveBeenCalledWith(66));
    expect(await screen.findByTestId('screen-field-layout-editor')).toBeInTheDocument();
  });

  test('stages the field counts for a screen picked on an unsaved edge', async () => {
    const onChanged = vi.fn();
    screens.getAllWithFields.mockResolvedValue([
      {
        id: 55,
        name: 'Resolution',
        fields: [
          { field_identifier: 'title', is_required: true },
          { field_identifier: 'priority', is_required: false },
          { field_identifier: 'description', is_required: false },
        ],
      },
    ]);
    render(TransitionInspector, {
      props: {
        transition: { edgeId: 'edge-1-2', from_status_id: 1, to_status_id: 2 },
        statuses: STATUSES,
        onClose: vi.fn(),
        onChanged,
      },
    });

    await screen.findByTestId('transition-inspector-no-screen');
    await fireEvent.change(screen.getByTestId('transition-inspector-screen-picker'), {
      target: { value: '55' },
    });

    await waitFor(() =>
      expect(onChanged).toHaveBeenCalledWith({
        edgeId: 'edge-1-2',
        screen: { id: 55, name: 'Resolution', field_count: 3, required_field_count: 1 },
      })
    );
  });
});
