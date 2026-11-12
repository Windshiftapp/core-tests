import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  update: vi.fn(),
  list: vi.fn(),
  create: vi.fn(),
  updateQueue: vi.fn(),
  remove: vi.fn(),
  reorder: vi.fn(),
}));

vi.mock('../../api/users.js', () => ({
  userPreferences: { get: mocks.get, update: mocks.update },
}));
vi.mock('../../api/queues.js', () => ({
  queues: {
    list: mocks.list,
    create: mocks.create,
    update: mocks.updateQueue,
    remove: mocks.remove,
    reorder: mocks.reorder,
  },
}));
vi.mock('../../stores/i18n.svelte.js', () => ({ t: (key) => key }));
vi.mock('../../stores/toasts.svelte.js', () => ({
  surfaceError: vi.fn(),
  successToast: vi.fn(),
  warningToast: vi.fn(),
}));
vi.mock('../../stores/workspaceDataStore.svelte.js', () => ({
  workspaceDataStore: { workspace: { key: 'WS' } },
}));
vi.mock('../../stores/collectionContext.svelte.js', () => ({
  collectionStore: { boardCollection: { created_by: 1 } },
}));
vi.mock('../../stores/workspacePermissions.svelte.js', () => ({
  workspacePermissions: { canAdminWorkspace: () => true },
}));
vi.mock('../../stores/auth.svelte.js', () => ({
  authStore: { currentUser: { id: 1 } },
}));
vi.mock('./FilterBuilderModal.svelte', async () => ({
  default: (await import('./FilterBuilderModalStub.svelte')).default,
}));

const { collectionPreferences } = await import('../../stores/collectionPreferences.svelte.js');
const { default: FilterMenu } = await import('./FilterMenu.svelte');

const ACTIVE_ROWS = [
  { field: { id: 'priority', type: 'text' }, operator: '=', value: 'High' },
];

beforeEach(() => {
  collectionPreferences.reset();
  localStorage.clear();
  vi.clearAllMocks();
  mocks.get.mockResolvedValue({ view_state: {}, saved_filters: {} });
  mocks.update.mockResolvedValue({});
  mocks.list.mockResolvedValue([]);
  mocks.create.mockResolvedValue({});
});

afterEach(() => {
  cleanup();
  localStorage.clear();
  collectionPreferences.reset();
});

function renderMenu(props = {}) {
  return render(FilterMenu, {
    workspaceId: 1,
    collectionId: 5,
    activeRows: ACTIVE_ROWS,
    applied: true,
    ...props,
  });
}

async function openMenu(props = {}) {
  renderMenu(props);
  await fireEvent.click(screen.getByTestId('dynamic-filter-toggle'));
  await screen.findByTestId('filter-menu');
}

describe('FilterMenu personal filters', () => {
  it('falls back to the legacy localStorage filters when the server has none', async () => {
    localStorage.setItem(
      'board-saved-filters:collection-5',
      JSON.stringify([{ id: 'legacy', name: 'Legacy', ql: 'id > 0', rows: [], pinned: false }])
    );
    await collectionPreferences.ensureLoaded(1);

    await openMenu();

    expect(await screen.findByTestId('filter-menu-personal-apply-legacy')).toBeInTheDocument();
  });

  it('prefers the server filters over the localStorage fallback', async () => {
    localStorage.setItem(
      'board-saved-filters:collection-5',
      JSON.stringify([{ id: 'legacy', name: 'Legacy', ql: 'id > 0', rows: [], pinned: false }])
    );
    mocks.get.mockResolvedValue({
      view_state: {},
      saved_filters: {
        'collection-5': [{ id: 'server', name: 'Server', ql: 'id > 1', rows: [], pinned: false }],
      },
    });
    await collectionPreferences.ensureLoaded(1);

    await openMenu();

    expect(await screen.findByTestId('filter-menu-personal-apply-server')).toBeInTheDocument();
    expect(screen.queryByTestId('filter-menu-personal-apply-legacy')).not.toBeInTheDocument();
  });

  it('applies a saved personal filter with its stored rows and query', async () => {
    const onapply = vi.fn();
    mocks.get.mockResolvedValue({
      view_state: {},
      saved_filters: {
        'collection-5': [
          {
            id: 'server',
            name: 'Server',
            ql: '`priority` = "Low"',
            rows: [{ field: { id: 'priority' }, operator: '=', value: 'Low' }],
            pinned: false,
          },
        ],
      },
    });
    await collectionPreferences.ensureLoaded(1);

    await openMenu({ onapply });
    await fireEvent.click(screen.getByTestId('filter-menu-personal-apply-server'));

    expect(onapply).toHaveBeenCalledWith(
      [{ field: { id: 'priority' }, operator: '=', value: 'Low' }],
      '`priority` = "Low"'
    );
  });

  it('saves a filter built in the modal to user preferences without writing localStorage', async () => {
    await collectionPreferences.ensureLoaded(1);
    await openMenu();

    await fireEvent.click(screen.getByTestId('filter-menu-add'));
    await fireEvent.click(await screen.findByTestId('stub-save-personal'));
    await collectionPreferences.flush();

    const saved = collectionPreferences.getSavedFilters(1, 5);
    expect(saved).toHaveLength(1);
    expect(saved[0]).toMatchObject({ name: 'Stub', ql: '`priority` = "High"' });
    expect(mocks.update).toHaveBeenCalledWith({
      saved_filters: {
        'collection-5': [expect.objectContaining({ name: 'Stub', ql: '`priority` = "High"' })],
      },
    });
    expect(localStorage.getItem('board-saved-filters:collection-5')).toBeNull();
  });

  it('opens the builder in save mode when a filter is active', async () => {
    await collectionPreferences.ensureLoaded(1);
    await openMenu();

    await fireEvent.click(screen.getByTestId('filter-menu-save'));

    const stub = await screen.findByTestId('filter-builder-stub');
    expect(stub.dataset.saveMode).toBe('true');
  });

  it('treats a null-operator row as an active filter', async () => {
    const nullRows = [
      { field: { id: 'cf_Risk level', type: 'select' }, operator: 'IS NULL', value: '', values: [] },
    ];
    await collectionPreferences.ensureLoaded(1);
    await openMenu({ activeRows: nullRows, applied: false });

    expect(screen.getByTestId('filter-menu-save')).toBeInTheDocument();
    expect(screen.getByTestId('dynamic-filter-toggle')).toHaveTextContent('1');
  });
});

describe('FilterMenu active filter visibility', () => {
  it('lists the applied rows and clears them', async () => {
    const onclear = vi.fn();
    await collectionPreferences.ensureLoaded(1);
    await openMenu({ onclear });

    expect(screen.getByTestId('filter-menu-active')).toBeInTheDocument();
    expect(screen.getByTestId('filter-menu-active-row-0')).toHaveTextContent('priority = High');

    await fireEvent.click(screen.getByTestId('filter-menu-clear'));
    expect(onclear).toHaveBeenCalledTimes(1);
  });

  it('marks the applied saved personal filter', async () => {
    mocks.get.mockResolvedValue({
      view_state: {},
      saved_filters: {
        'collection-5': [
          {
            id: 'server',
            name: 'Server',
            ql: '`priority` = "High"',
            rows: [{ field: { id: 'priority', type: 'text' }, operator: '=', value: 'High' }],
            pinned: false,
          },
        ],
      },
    });
    await collectionPreferences.ensureLoaded(1);
    await openMenu();

    expect(await screen.findByTestId('filter-menu-personal-applied-server')).toBeInTheDocument();
  });

  it('clears the live filter when the applied personal filter is deleted', async () => {
    const onclear = vi.fn();
    mocks.get.mockResolvedValue({
      view_state: {},
      saved_filters: {
        'collection-5': [
          {
            id: 'server',
            name: 'Server',
            ql: '`priority` = "High"',
            rows: [{ field: { id: 'priority', type: 'text' }, operator: '=', value: 'High' }],
            pinned: false,
          },
        ],
      },
    });
    await collectionPreferences.ensureLoaded(1);
    await openMenu({ onclear });

    await fireEvent.click(await screen.findByTestId('filter-menu-personal-delete-server'));
    expect(onclear).toHaveBeenCalledTimes(1);
  });

  it('keeps the live filter when a different personal filter is deleted', async () => {
    const onclear = vi.fn();
    mocks.get.mockResolvedValue({
      view_state: {},
      saved_filters: {
        'collection-5': [
          {
            id: 'other',
            name: 'Other',
            ql: '`priority` = "Low"',
            rows: [{ field: { id: 'priority', type: 'text' }, operator: '=', value: 'Low' }],
            pinned: false,
          },
        ],
      },
    });
    await collectionPreferences.ensureLoaded(1);
    await openMenu({ onclear });

    await fireEvent.click(await screen.findByTestId('filter-menu-personal-delete-other'));
    expect(onclear).not.toHaveBeenCalled();
  });
});

describe('FilterMenu shared filters', () => {
  it('lists shared views and applies one', async () => {
    const onapply = vi.fn();
    mocks.list.mockResolvedValue([
      {
        id: 9,
        name: 'Team triage',
        ql: '`status` = "Open"',
        filter_state: JSON.stringify([
          { field: { id: 'status' }, operator: '=', value: 'Open' },
        ]),
        builtin: false,
      },
    ]);

    await openMenu({ onapply });
    await fireEvent.click(await screen.findByTestId('filter-menu-shared-apply-9'));

    expect(onapply).toHaveBeenCalledWith(
      [{ field: { id: 'status' }, operator: '=', value: 'Open' }],
      '`status` = "Open"'
    );
  });

  it('saves a shared filter through the queues API when the user can author', async () => {
    await openMenu();

    await fireEvent.click(screen.getByTestId('filter-menu-add'));
    const stub = await screen.findByTestId('filter-builder-stub');
    expect(stub.dataset.canShare).toBe('true');

    await fireEvent.click(screen.getByTestId('stub-save-shared'));

    expect(mocks.create).toHaveBeenCalledWith(
      'WS',
      5,
      expect.objectContaining({ name: 'Stub', ql_query: '`priority` = "High"' })
    );
  });

  it('clears the live filter when the applied shared view is deleted', async () => {
    const onclear = vi.fn();
    mocks.list.mockResolvedValue([
      {
        id: 9,
        name: 'Team triage',
        ql: '`priority` = "High"',
        filter_state: JSON.stringify([
          { field: { id: 'priority', type: 'text' }, operator: '=', value: 'High' },
        ]),
        builtin: false,
      },
    ]);

    await openMenu({ onclear });
    await fireEvent.click(await screen.findByTestId('filter-menu-shared-delete-9'));

    await vi.waitFor(() => expect(onclear).toHaveBeenCalledTimes(1));
  });

  it('reorders shared views through the queues API', async () => {
    mocks.list.mockResolvedValue([
      { id: 9, name: 'First', ql: '`priority` = "High"', filter_state: null, builtin: false },
      { id: 10, name: 'Second', ql: '`priority` = "Low"', filter_state: null, builtin: false },
    ]);

    await openMenu();
    await fireEvent.click(await screen.findByTestId('filter-menu-shared-down-9'));

    await vi.waitFor(() =>
      expect(mocks.reorder).toHaveBeenCalledWith('WS', 5, [10, 9])
    );
  });

  it('renames a shared view through the queues API', async () => {
    mocks.list.mockResolvedValue([
      { id: 9, name: 'Team triage', ql: '`status` = "Open"', filter_state: null, builtin: false },
    ]);

    await openMenu();
    await fireEvent.click(await screen.findByTestId('filter-menu-shared-rename-9'));
    const input = await screen.findByTestId('filter-menu-shared-rename-input-9');
    await fireEvent.input(input, { target: { value: 'Renamed triage' } });
    await fireEvent.click(screen.getByTestId('filter-menu-shared-rename-save-9'));

    await vi.waitFor(() =>
      expect(mocks.updateQueue).toHaveBeenCalledWith(9, {
        name: 'Renamed triage',
        ql_query: '`status` = "Open"',
        filter_state: null,
      })
    );
  });
});

describe('FilterMenu personal filter management', () => {
  function personalFilter(overrides = {}) {
    return {
      id: 'server',
      name: 'Server',
      ql: '`priority` = "High"',
      rows: [{ field: { id: 'priority', type: 'text' }, operator: '=', value: 'High' }],
      pinned: false,
      ...overrides,
    };
  }

  it('renames a personal filter and persists it', async () => {
    mocks.get.mockResolvedValue({
      view_state: {},
      saved_filters: { 'collection-5': [personalFilter()] },
    });
    await collectionPreferences.ensureLoaded(1);
    await openMenu();

    await fireEvent.click(await screen.findByTestId('filter-menu-personal-rename-server'));
    const input = await screen.findByTestId('filter-menu-personal-rename-input-server');
    await fireEvent.input(input, { target: { value: 'Renamed server' } });
    await fireEvent.click(screen.getByTestId('filter-menu-personal-rename-save-server'));

    expect(collectionPreferences.getSavedFilters(1, 5)[0].name).toBe('Renamed server');
  });

  it('pins a personal filter and persists it', async () => {
    mocks.get.mockResolvedValue({
      view_state: {},
      saved_filters: { 'collection-5': [personalFilter()] },
    });
    await collectionPreferences.ensureLoaded(1);
    await openMenu();

    await fireEvent.click(await screen.findByTestId('filter-menu-personal-pin-server'));

    expect(screen.getByTestId('filter-menu-personal-pin-server')).toHaveAttribute(
      'aria-pressed',
      'true'
    );
    expect(collectionPreferences.getSavedFilters(1, 5)[0].pinned).toBe(true);
  });
});
