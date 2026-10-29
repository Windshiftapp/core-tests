import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeAll, beforeEach, describe, expect, test, vi } from 'vitest';
import { items } from '../../api/items.js';
import { workspaces } from '../../api/workspaces.js';
import { getAssignableUsers } from '../../api/users.js';
import { iterations, milestones } from '../../api/milestones.js';
import { links, priorities } from '../../api/configuration.js';
import { customerOrganisations, portalCustomers } from '../../api/portal.js';
import { assets } from '../../api/assets.js';









vi.mock('../../api/items.js', () => ({
  items: {
        update: vi.fn(async (id, values) => ({ id, ...values })),
        transition: vi.fn(async (id, statusId) => ({ id, status_id: statusId })),
      },
}))

vi.mock('../../api/workspaces.js', () => ({
  workspaces: {
        getStatuses: vi.fn(),
        getProjects: vi.fn(),
        get: vi.fn(),
      },
}))

vi.mock('../../api/users.js', () => ({
  getAssignableUsers: vi.fn(),
}))

vi.mock('../../api/milestones.js', () => ({
  milestones: { getAll: vi.fn() },
  iterations: { getAll: vi.fn() },
  milestoneCategories: { getAll: vi.fn().mockResolvedValue([]) },
}))

vi.mock('../../api/configuration.js', () => ({
  priorities: { getAll: vi.fn() },
  links: { getForItems: vi.fn(async () => ({})) },
}))

vi.mock('../../api/portal.js', () => ({
  portalCustomers: { getAll: vi.fn() },
  customerOrganisations: { getAll: vi.fn() },
}))

vi.mock('../../api/misc.js', () => ({}))

vi.mock('../../api/assets.js', () => ({
  assets: { getAll: vi.fn() },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
  i18n: { locale: 'en-US' },
}));

beforeAll(() => {
  if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {};
  if (!globalThis.ResizeObserver) {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  }
  if (!globalThis.requestAnimationFrame) {
    globalThis.requestAnimationFrame = (callback) => { queueMicrotask(() => callback(performance.now())); };
  }
});


const { collectionEditorOptions } = await import('../../stores/collectionEditorOptions.svelte.js');
const { collectionFieldLinks } = await import('../../stores/collectionFieldLinks.svelte.js');
const { default: ListCellRenderer } = await import('./ListCellRenderer.svelte');

const assigneeColumn = { field_type: 'system', field_identifier: 'assignee' };

function renderAssignee(itemId, workspaceId) {
  return render(ListCellRenderer, {
    props: {
      item: {
        id: itemId,
        workspace_id: workspaceId,
        title: `Item ${itemId}`,
        assignee_id: null,
        custom_field_values: {},
      },
      column: assigneeColumn,
      workspace: { id: workspaceId },
      canEdit: true,
    },
  });
}

async function openAssignee(itemId) {
  await fireEvent.click(screen.getByTestId(`list-cell-assignee-${itemId}`));
}

describe('ListCellRenderer collection option loading (WI-630)', () => {
  beforeEach(() => {
    collectionEditorOptions.reset();
    collectionFieldLinks.reset();
  });

  afterEach(() => {
    cleanup();
    document.body.innerHTML = '';
  });

  test('loads nothing until an editable cell is opened and single-flights same-workspace rows', async () => {
    let resolveUsers;
    getAssignableUsers.mockReturnValue(
      new Promise((resolve) => {
        resolveUsers = resolve;
      })
    );

    renderAssignee(101, 11);
    renderAssignee(102, 11);

    expect(getAssignableUsers).not.toHaveBeenCalled();

    await openAssignee(101);
    await openAssignee(102);

    await waitFor(() => expect(getAssignableUsers).toHaveBeenCalledTimes(1));
    expect(getAssignableUsers).toHaveBeenCalledWith(11);

    resolveUsers([{ id: 1101, first_name: 'Eleven', last_name: 'User' }]);
    await waitFor(() => {
      expect(screen.getAllByTestId('user-picker-option-1101')).toHaveLength(2);
    });
  });

  test('uses the owning workspace for each row in a mixed-workspace collection', async () => {
    getAssignableUsers.mockImplementation(async (workspaceId) => [
      {
        id: workspaceId * 100,
        first_name: `Workspace ${workspaceId}`,
        last_name: 'User',
      },
    ]);

    renderAssignee(201, 11);
    renderAssignee(202, 22);

    await openAssignee(201);
    await waitFor(() => expect(screen.getByTestId('user-picker-option-1100')).toBeInTheDocument());
    await fireEvent.click(screen.getByTestId('user-picker-option-1100'));

    await openAssignee(202);
    await waitFor(() => expect(screen.getByTestId('user-picker-option-2200')).toBeInTheDocument());

    expect(getAssignableUsers).toHaveBeenCalledTimes(2);
    expect(getAssignableUsers).toHaveBeenNthCalledWith(1, 11);
    expect(getAssignableUsers).toHaveBeenNthCalledWith(2, 22);
    expect(collectionEditorOptions.get(11).users[0].id).toBe(1100);
    expect(collectionEditorOptions.get(22).users[0].id).toBe(2200);
  });

  test('reuses cached options after a row is unmounted and rendered again', async () => {
    getAssignableUsers.mockResolvedValue([
      { id: 3300, first_name: 'Cached', last_name: 'User' },
    ]);

    const firstPage = renderAssignee(301, 33);
    await openAssignee(301);
    await waitFor(() => expect(screen.getByTestId('user-picker-option-3300')).toBeInTheDocument());
    firstPage.unmount();
    document.body.innerHTML = '';

    renderAssignee(302, 33);
    await openAssignee(302);
    await waitFor(() => expect(screen.getByTestId('user-picker-option-3300')).toBeInTheDocument());

    expect(getAssignableUsers).toHaveBeenCalledTimes(1);
  });
});

describe('ListCellRenderer item type icon', () => {
  afterEach(() => cleanup());

  test('renders the server-provided type icon when the workspace catalog omits the type', () => {
    render(ListCellRenderer, {
      props: {
        item: {
          id: 701,
          workspace_id: 11,
          title: 'Initiative row',
          item_type_id: 1,
          item_type_name: 'Initiative',
          item_type_icon: 'Target',
          item_type_color: '#7c3aed',
          custom_field_values: {},
        },
        column: { field_type: 'system', field_identifier: 'title' },
        workspace: { id: 11 },
        // The workspace catalog intentionally omits types outside the effective
        // configuration set; the row must still render the real icon.
        itemTypes: [{ id: 2, name: 'Epic', icon: 'Zap', color: '#2563eb' }],
        canEdit: false,
      },
    });

    const icon = screen.getByTestId('list-cell-type-icon-701');
    expect(icon.querySelector('svg')).toHaveClass('lucide-target');
    expect(icon.querySelector('svg')).not.toHaveClass('lucide-file-text');
    expect(icon).toHaveAttribute('title', 'Initiative');
    expect(screen.getByTestId('workspace-item-title-701')).toHaveTextContent('Initiative row');
  });
});

// Custom field updates merge per field server-side. The renderer sends only
// the edited field's key; the item's other values are preserved by the server
// and must not be sent from a possibly stale row prop.
describe('ListCellRenderer custom-field edits', () => {
  beforeEach(() => {
    collectionEditorOptions.reset();
    collectionFieldLinks.reset();
  });

  afterEach(() => {
    cleanup();
    document.body.innerHTML = '';
  });

  test('editing one cell sends only that field', async () => {
    render(ListCellRenderer, {
      props: {
        item: {
          id: 601,
          workspace_id: 11,
          title: 'Item 601',
          custom_field_values: { 11: 'stored text', 22: 'keep me', 33: 42 },
        },
        column: { field_type: 'custom', field_identifier: '11' },
        customFieldDefinitions: [
          { id: 11, name: 'Text field', field_type: 'text' },
          { id: 22, name: 'Keep field', field_type: 'text' },
          { id: 33, name: 'Number field', field_type: 'number' },
        ],
        workspace: { id: 11 },
        canEdit: true,
      },
    });

    await fireEvent.click(screen.getByTestId('list-custom-field-11-601'));
    const input = screen.getByTestId('custom-field-input-11');
    await fireEvent.input(input, { target: { value: 'typed here' } });
    await fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => expect(items.update).toHaveBeenCalledTimes(1));
    expect(items.update).toHaveBeenCalledWith(601, {
      custom_field_values: { 11: 'typed here' },
    });
  });

  test('two rapid cell edits each send their own single field', async () => {
    items.update.mockImplementation(async (id, values) => ({ id, ...values }));

    render(ListCellRenderer, {
      props: {
        item: {
          id: 501,
          workspace_id: 11,
          title: 'Item 501',
          custom_field_values: {},
        },
        column: { field_type: 'custom', field_identifier: '11' },
        customFieldDefinitions: [
          { id: 11, name: 'Text field', field_type: 'text' },
          { id: 12, name: 'Other field', field_type: 'text' },
        ],
        workspace: { id: 11 },
        canEdit: true,
      },
    });

    await fireEvent.click(screen.getByTestId('list-custom-field-11-501'));
    const input = screen.getByTestId('custom-field-input-11');
    await fireEvent.input(input, { target: { value: 'typed here' } });
    await fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => expect(items.update).toHaveBeenCalledTimes(1));
    expect(items.update).toHaveBeenLastCalledWith(501, {
      custom_field_values: { 11: 'typed here' },
    });
  });
});

// Start and end dates are optional system columns, mirroring the due-date cell.
describe('ListCellRenderer start and end date fields', () => {
  beforeEach(() => {
    collectionEditorOptions.reset();
    collectionFieldLinks.reset();
    items.update.mockClear();
    items.update.mockImplementation(async (id, values) => ({ id, ...values }));
  });

  afterEach(() => {
    cleanup();
    document.body.innerHTML = '';
  });

  function renderDateCell(item, identifier, canEdit = true) {
    return render(ListCellRenderer, {
      props: {
        item: { workspace_id: 11, custom_field_values: {}, ...item },
        column: { field_type: 'system', field_identifier: identifier },
        workspace: { id: 11 },
        canEdit,
      },
    });
  }

  test('renders a read-only start date', () => {
    renderDateCell({ id: 901, title: 'Item 901', start_date: '2026-03-15' }, 'start_date', false);
    expect(screen.getByText(/2026/)).toBeInTheDocument();
  });

  test('renders a read-only end date', () => {
    renderDateCell({ id: 902, title: 'Item 902', end_date: '2026-04-20' }, 'end_date', false);
    expect(screen.getByText(/2026/)).toBeInTheDocument();
  });

  test('edits the start date and persists only that field', async () => {
    renderDateCell({ id: 903, title: 'Item 903', start_date: null }, 'start_date');

    await fireEvent.click(screen.getByTestId('inline-date-display'));
    const input = document.querySelector('input[type="date"]');
    await fireEvent.input(input, { target: { value: '2026-05-01' } });
    await fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => expect(items.update).toHaveBeenCalledTimes(1));
    expect(items.update).toHaveBeenCalledWith(903, { start_date: '2026-05-01' });
  });

  test('edits the end date and persists only that field', async () => {
    renderDateCell({ id: 904, title: 'Item 904', end_date: null }, 'end_date');

    await fireEvent.click(screen.getByTestId('inline-date-display'));
    const input = document.querySelector('input[type="date"]');
    await fireEvent.input(input, { target: { value: '2026-06-02' } });
    await fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => expect(items.update).toHaveBeenCalledTimes(1));
    expect(items.update).toHaveBeenCalledWith(904, { end_date: '2026-06-02' });
  });
});

// Story points and estimate are system fields, not custom fields, so they edit
// through the generic field/fieldType path rather than the custom-field one.
describe('ListCellRenderer built-in number and duration fields', () => {
  beforeEach(() => {
    collectionEditorOptions.reset();
    collectionFieldLinks.reset();
    items.update.mockClear();
    items.update.mockImplementation(async (id, values) => ({ id, ...values }));
  });

  afterEach(() => {
    cleanup();
    document.body.innerHTML = '';
  });

  function renderCell(item, column, canEdit = true) {
    return render(ListCellRenderer, {
      props: {
        item,
        column,
        workspace: { id: 11 },
        canEdit,
      },
    });
  }

  const storyPointsColumn = { field_type: 'system', field_identifier: 'story_points' };
  const estimateColumn = { field_type: 'system', field_identifier: 'estimate' };

  test('edits story points through the number editor', async () => {
    renderCell(
      { id: 801, workspace_id: 11, title: 'Item 801', story_points: 3, custom_field_values: {} },
      storyPointsColumn
    );

    expect(screen.getByTestId('inline-number-value')).toHaveTextContent('3');

    await fireEvent.click(screen.getByTestId('inline-number-display'));
    const input = screen.getByTestId('inline-number-input');
    await fireEvent.input(input, { target: { value: '5' } });
    await fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => expect(items.update).toHaveBeenCalledTimes(1));
    expect(items.update).toHaveBeenCalledWith(801, { story_points: 5 });
  });

  test('clears story points with an empty value', async () => {
    renderCell(
      { id: 802, workspace_id: 11, title: 'Item 802', story_points: 3, custom_field_values: {} },
      storyPointsColumn
    );

    await fireEvent.click(screen.getByTestId('inline-number-display'));
    const input = screen.getByTestId('inline-number-input');
    await fireEvent.input(input, { target: { value: '' } });
    await fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => expect(items.update).toHaveBeenCalledTimes(1));
    expect(items.update).toHaveBeenCalledWith(802, { story_points: null });
  });

  test('rejects a negative story points value without saving', async () => {
    renderCell(
      { id: 803, workspace_id: 11, title: 'Item 803', story_points: 3, custom_field_values: {} },
      storyPointsColumn
    );

    await fireEvent.click(screen.getByTestId('inline-number-display'));
    const input = screen.getByTestId('inline-number-input');
    await fireEvent.input(input, { target: { value: '-1' } });
    await fireEvent.keyDown(input, { key: 'Enter' });

    expect(items.update).not.toHaveBeenCalled();
    expect(screen.getByTestId('inline-number-input')).toBeInTheDocument();
  });

  test('edits estimate as a duration string', async () => {
    renderCell(
      { id: 804, workspace_id: 11, title: 'Item 804', estimate_minutes: 60, custom_field_values: {} },
      estimateColumn
    );

    expect(screen.getByTestId('inline-duration-value')).toHaveTextContent('1h');

    await fireEvent.click(screen.getByTestId('inline-duration-display'));
    const input = screen.getByTestId('inline-duration-input');
    await fireEvent.input(input, { target: { value: '2h 30m' } });
    await fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => expect(items.update).toHaveBeenCalledTimes(1));
    expect(items.update).toHaveBeenCalledWith(804, { estimate_minutes: 150 });
  });

  test('renders read-only values when the user cannot edit', () => {
    renderCell(
      {
        id: 805,
        workspace_id: 11,
        title: 'Item 805',
        story_points: 8,
        estimate_minutes: 90,
        custom_field_values: {},
      },
      storyPointsColumn,
      false
    );
    renderCell(
      {
        id: 806,
        workspace_id: 11,
        title: 'Item 806',
        story_points: 8,
        estimate_minutes: 90,
        custom_field_values: {},
      },
      estimateColumn,
      false
    );

    expect(screen.queryByTestId('inline-number-input')).not.toBeInTheDocument();
    expect(screen.queryByTestId('inline-duration-input')).not.toBeInTheDocument();
    expect(screen.getByText('8')).toBeInTheDocument();
    expect(screen.getByText('1h 30m')).toBeInTheDocument();
  });
});
