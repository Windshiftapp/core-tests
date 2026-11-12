import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, test, vi } from 'vitest';

// SvelteFlow cannot lay out edges in jsdom, so stand in for @xyflow/svelte and
// drive the same callbacks the library does: onedgeclick receives
// `{ event, edge }`, onconnect receives the connection params.
vi.mock('@xyflow/svelte', async () => {
  const { default: SvelteFlow } = await import('./__stubs__/SvelteFlowStub.svelte');
  const { default: Noop } = await import('./__stubs__/Noop.svelte');
  return {
    SvelteFlow,
    Controls: Noop,
    MiniMap: Noop,
    Background: Noop,
    Handle: Noop,
    BaseEdge: Noop,
    EdgeReconnectAnchor: Noop,
    getSmoothStepPath: () => ['M0 0', 0, 0, 0, 0],
    BackgroundVariant: { Dots: 'dots' },
    ConnectionMode: { Loose: 'loose' },
    Position: { Left: 'left', Right: 'right', Top: 'top', Bottom: 'bottom' },
    addEdge: (edge, edges) => [...edges, edge],
  };
});

vi.mock('../../api/workflows.js', () => ({
  statuses: { create: vi.fn() },
}));

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

import { screens } from '../../api/configuration.js';
import { transitions } from '../../api/transitions.js';
import { successToast } from '../../stores/toasts.svelte.js';
import { flowTestState } from './__stubs__/flowTestState.js';
import SvelteFlowDesigner from './SvelteFlowDesigner.svelte';

const STATUSES = [
  { id: 1, name: 'Open', category_color: '#111111', category_name: 'To Do' },
  { id: 2, name: 'In Progress', category_color: '#222222', category_name: 'In Progress' },
  { id: 3, name: 'Done', category_color: '#333333', category_name: 'Done' },
];

function makeWorkflow() {
  return {
    id: 7,
    name: 'Delivery',
    transitions: [
      { id: 101, workflow_id: 7, from_status_id: 1, to_status_id: 2, display_order: 0 },
      { id: 102, workflow_id: 7, from_status_id: 2, to_status_id: 3, display_order: 1 },
    ],
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  flowTestState.connectPayload = null;
  transitions.governance.mockResolvedValue({ screen: null });
  transitions.setScreen.mockResolvedValue({ screen: null });
  screens.getAllWithFields.mockResolvedValue([{ id: 55, name: 'Resolution' }]);
});

async function renderDesigner(overrides = {}) {
  const props = {
    workflow: makeWorkflow(),
    statuses: STATUSES,
    statusCategories: [],
    onSave: vi.fn().mockResolvedValue([]),
    onSaved: vi.fn(),
    onCancel: vi.fn(),
    ...overrides,
  };
  render(SvelteFlowDesigner, { props });
  await waitFor(() => expect(screen.getByTestId('flow-edge-edge-1-2')).toBeInTheDocument());
  return props;
}

describe('SvelteFlowDesigner transition inspector', () => {
  test('opens the inspector when the library reports an edge click', async () => {
    await renderDesigner();

    await fireEvent.click(screen.getByTestId('flow-edge-edge-1-2'));

    await waitFor(() => expect(screen.getByTestId('transition-inspector')).toBeInTheDocument());
    expect(screen.getByTestId('transition-inspector-title')).toHaveTextContent(
      'Open → In Progress'
    );
    expect(screen.queryByTestId('transition-inspector-pending')).toBeNull();
  });

  test('stages a screen on a new transition and binds it after save', async () => {
    const persisted = [
      { id: 101, from: { id: 1 }, to: { id: 2 }, from_all_statuses: false },
      { id: 102, from: { id: 2 }, to: { id: 3 }, from_all_statuses: false },
      { id: 999, from: { id: 1 }, to: { id: 3 }, from_all_statuses: false },
    ];
    const props = await renderDesigner({ onSave: vi.fn().mockResolvedValue(persisted) });

    flowTestState.connectPayload = {
      source: 'status-1',
      target: 'status-3',
      sourceHandle: 'right',
      targetHandle: 'target-left',
    };
    await fireEvent.click(screen.getByTestId('flow-connect'));
    const newEdge = await screen.findByTestId('flow-edge-edge-1-3');

    await fireEvent.click(newEdge);
    await waitFor(() => expect(screen.getByTestId('transition-inspector')).toBeInTheDocument());
    expect(screen.getByTestId('transition-inspector-pending')).toBeInTheDocument();

    await fireEvent.change(screen.getByTestId('transition-inspector-screen-picker'), {
      target: { value: '55' },
    });
    await waitFor(() => expect(successToast).toHaveBeenCalled());
    expect(transitions.setScreen).not.toHaveBeenCalled();
    expect(screen.getByTestId('transition-inspector-screen-name')).toHaveTextContent('Resolution');

    await fireEvent.click(screen.getByTestId('workflow-save'));

    await waitFor(() => expect(transitions.setScreen).toHaveBeenCalledWith(999, 55));
    expect(props.onSaved).toHaveBeenCalled();
  });
});
