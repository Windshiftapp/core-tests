import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeAll, beforeEach, describe, expect, test, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  fetchV2Data: vi.fn(),
  getAll: vi.fn(),
  bulkUpdate: vi.fn(),
  bulkTransition: vi.fn(),
  bulkDelete: vi.fn(),
  getAllTeams: vi.fn(),
  getCollection: vi.fn(),
  getBoardConfiguration: vi.fn(async () => null),
  createBoardConfiguration: vi.fn(async (_collectionId, _workspaceId, data) => ({ id: 99, ...data })),
  updateBoardConfiguration: vi.fn(async (_collectionId, _configId, data) => ({ id: 99, ...data })),
  invalidateBoardConfiguration: vi.fn(),
  getCatalog: vi.fn(async () => ({ fields: [], logical_operators: [] })),
  canEdit: vi.fn(() => false),
  canAdmin: true,
  confirm: vi.fn(async () => true),
  currentUser: { id: 7, name: 'Agent' },
}));

vi.mock('../../api/items.js', () => ({
  items: {
        getAll: mocks.getAll,
        bulkUpdate: mocks.bulkUpdate,
        bulkTransition: mocks.bulkTransition,
        bulkDelete: mocks.bulkDelete,
        update: vi.fn(async (id, values) => ({ id, ...values })),
        transition: vi.fn(async (id, statusId) => ({ id, status_id: statusId })),
        delete: vi.fn(),
        links: { getForItems: vi.fn(async () => ({})) },
      },
}))

vi.mock('../../api/teams.js', () => ({
  teams: { getAll: mocks.getAllTeams },
}))

vi.mock('../../api/collections.js', () => ({
  collections: {
        get: mocks.getCollection,
        getBoardConfiguration: mocks.getBoardConfiguration,
        createBoardConfiguration: mocks.createBoardConfiguration,
        updateBoardConfiguration: mocks.updateBoardConfiguration,
      },
  collectionCategories: { getAll: vi.fn().mockResolvedValue([]) },
}))

vi.mock('../../api/queryLanguage.js', () => ({
  queryLanguage: { getCatalog: mocks.getCatalog, getValues: vi.fn(async () => []) },
}));

vi.mock('../../api/core.js', () => ({
  fetchV2Data: mocks.fetchV2Data,
  fetchAPI: vi.fn(),
  fetchAllV2Pages: vi.fn().mockResolvedValue([]),
  setAPIRequestSessionKey: vi.fn(),
}));

vi.mock('../../composables/useConfirm.js', () => ({
  confirm: mocks.confirm,
}));

vi.mock('../../stores/auth.svelte.js', () => ({
  authStore: {
    get currentUser() {
      return mocks.currentUser;
    },
  },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
  i18n: { locale: 'en-US' },
}));

vi.mock('../../stores/toasts.svelte.js', () => ({
  errorToast: vi.fn(),
  successToast: vi.fn(),
}));

vi.mock('../../router.js', () => ({
  navigate: vi.fn(),
}));

vi.mock('../../stores/auth.svelte.js', () => ({
  authStore: {
      get currentUser() {
        return mocks.currentUser;
      },
    },
}))

vi.mock('../../stores/collectionContext.js', () => ({
  collectionStore: {
      invalidateBoardConfiguration: mocks.invalidateBoardConfiguration,
    },
}))

vi.mock('../../stores/workspaceDataStore.svelte.js', () => ({
  workspaceDataStore: {
      initialized: true,
      workspaceId: 1,
      workspace: { id: 1, key: 'SQA', name: 'Queue WS' },
      users: [{ id: 7, name: 'Agent' }],
      statuses: [{ id: 2, name: 'In Progress', category_id: 2, is_completed: false }],
      statusCategories: [{ id: 2, name: 'In Progress', color: '#2563eb' }],
      priorities: [{ id: 3, name: 'High', color: '#dc2626' }],
      milestones: [],
      iterations: [],
      projects: [],
      itemTypes: [],
      customFieldDefinitions: [],
      initialize: vi.fn().mockResolvedValue(undefined),
    },
}));

vi.mock('../../stores/workspaceGradient.svelte.js', () => ({
  useGradientStyles: () => ({
    backgroundStyle: '',
    contextVars: '',
    tableStyle: () => '',
    tableHeaderStyle: '',
    glassStyle: () =>
      'background-color: var(--ds-glass-bg); border-color: var(--ds-glass-border); backdrop-filter: blur(12px);',
    glassTextStyle: 'color: var(--ds-text);',
  }),
}));

vi.mock('../../stores/workspacePermissions.svelte.js', () => ({
  workspacePermissions: {
    get canEdit() {
      return mocks.canEdit;
    },
    canAdminWorkspace: () => mocks.canAdmin,
  },
}));

vi.mock('../../stores/collectionEditorOptions.svelte.js', () => ({
  collectionEditorOptions: {
    prime: vi.fn(),
    get: () => ({
      statuses: [],
      users: [],
      priorities: [],
      projects: [],
      milestones: [],
      iterations: [],
      loading: {},
    }),
    load: vi.fn(),
  },
}));

vi.mock('../../stores/collectionFieldLinks.svelte.js', () => ({
  collectionFieldLinks: {
    getFieldLinks: () => [],
    loadForItems: vi.fn(),
    refreshForItems: vi.fn(),
  },
}));

import SupportQueue from './SupportQueue.svelte';
import { items } from '../../api/items.js';
import { teams } from '../../api/teams.js';
import { collections } from '../../api/collections.js';
import { queryLanguage } from '../../api/queryLanguage.js';
import { fetchAPI, fetchV2Data, setAPIRequestSessionKey } from '../../api/core.js';




beforeAll(() => {
  if (!globalThis.IntersectionObserver) {
    globalThis.IntersectionObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  }
  if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {};
  if (!globalThis.ResizeObserver) {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  }
});

const QUEUES = [
  { id: 0, key: 'unassigned', name: 'Unassigned', count: 2, ql: 'assignee = null', builtin: true },
  { id: 0, key: 'team-owned', name: 'Team-owned', count: 1, ql: 'team != null', builtin: true },
  {
    id: 42,
    name: 'My queue',
    count: 1,
    ql: 'cf_priority = 3',
    builtin: false,
    filter_state: '{"dynamicFields":[]}',
  },
];

const ROWS = [
  {
    id: 11,
    workspace_key: 'SQA',
    workspace_item_number: 101,
    title: 'Broken import',
    status_id: 2,
    priority_id: 3,
    assignee_id: null,
    due_date: null,
    updated_at: '2026-02-03T10:00:00Z',
  },
  {
    id: 12,
    workspace_key: 'SQA',
    workspace_item_number: 102,
    title: 'Lost attachment',
    status_id: 2,
    priority_id: null,
    assignee_id: null,
    due_date: null,
    updated_at: '2026-02-04T10:00:00Z',
  },
];

function deferred() {
  let resolve;
  const promise = new Promise((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
}

function itemsResponse(data, { page = 1, limit = 100, totalItems = data.length } = {}) {
  return {
    data,
    pagination: {
      page,
      page_size: limit,
      total_items: totalItems,
      total_pages: Math.max(1, Math.ceil(totalItems / limit)),
    },
  };
}

function mockApiResponses() {
  mocks.fetchV2Data.mockImplementation((url) => {
    if (url.includes('/queues')) return Promise.resolve(QUEUES);
    return Promise.resolve([]);
  });
  // The queue pages through the same /items document the list view uses, so
  // the mock filters and paginates the way the server does.
  mocks.getAll.mockImplementation((filters = {}) => {
    const search = String(filters.search ?? '').toLowerCase();
    const matching = search
      ? ROWS.filter((row) => row.title.toLowerCase().includes(search))
      : ROWS;
    const page = filters.page ?? 1;
    const limit = filters.limit ?? 100;
    const start = (page - 1) * limit;
    return Promise.resolve(
      itemsResponse(matching.slice(start, start + limit), {
        page,
        limit,
        totalItems: matching.length,
      })
    );
  });
}

// LazyRender gates rows behind IntersectionObserver, which the test DOM
// lacks; report the sentinel as immediately visible so rows render.
class ImmediateIntersectionObserver {
  constructor(callback) {
    this.callback = callback;
  }
  observe() {
    this.callback([{ isIntersecting: true }], this);
  }
  unobserve() {}
  disconnect() {}
  takeRecords() {
    return [];
  }
}

beforeEach(() => {
  vi.stubGlobal('IntersectionObserver', ImmediateIntersectionObserver);
  mockApiResponses();
  mocks.bulkUpdate.mockResolvedValue({});
  mocks.bulkTransition.mockResolvedValue({
    requested_count: 1,
    transitioned_count: 1,
    unchanged_count: 0,
    failed_count: 0,
    failures: [],
  });
  mocks.bulkDelete.mockResolvedValue({
    requested_count: 1,
    deleted_count: 1,
    failed_count: 0,
    failures: [],
  });
  mocks.getAllTeams.mockResolvedValue([]);
  mocks.getCollection.mockResolvedValue({ id: 5, created_by: 7 });
  mocks.getBoardConfiguration.mockResolvedValue(null);
  mocks.confirm.mockResolvedValue(true);
});

afterEach(() => {
  vi.unstubAllGlobals();
  cleanup();
  vi.clearAllMocks();
});

describe('SupportQueue list-shaped view', () => {
  test('renders the merged queue catalog and list-grid rows for the active queue', async () => {
    render(SupportQueue, { props: { workspaceId: 1 } });

    await waitFor(() =>
      expect(screen.getByTestId('support-queue-tab-unassigned')).toHaveAttribute(
        'aria-selected',
        'true',
      ),
    );
    expect(screen.getByTestId('support-queue-tab-42')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByTestId('support-queue-row-11')).toBeVisible());
    expect(screen.getByTestId('support-queue-row-12')).toBeVisible();

    // The shared list grid renders the item key cell inside each row.
    expect(screen.getByText('SQA-101')).toBeInTheDocument();
    expect(screen.getByText('SQA-102')).toBeInTheDocument();
    // With no board configuration the queue falls back to the shared list
    // defaults, so its columns stay consistent with the list view.
    expect(screen.getByText('Key')).toBeInTheDocument();
    expect(screen.getByText('Title')).toBeInTheDocument();
    expect(screen.getByText('Created')).toBeInTheDocument();
  });

  test('persists a column change through the shared board configuration and drops the list cache', async () => {
    render(SupportQueue, { props: { workspaceId: 1 } });
    await screen.findByTestId('support-queue-row-11');

    await fireEvent.click(screen.getByTestId('queue-column-selector-trigger'));
    await fireEvent.click(await screen.findByTestId('add-column-assignee'));
    await fireEvent.click(screen.getByTestId('column-selector-apply'));

    await waitFor(() => expect(mocks.createBoardConfiguration).toHaveBeenCalledTimes(1));
    const [collectionArg, workspaceArg, payload] = mocks.createBoardConfiguration.mock.calls[0];
    expect(collectionArg).toBeNull();
    expect(workspaceArg).toBe(1);
    expect(payload.list_columns).toEqual(
      expect.arrayContaining([expect.objectContaining({ field_identifier: 'assignee' })])
    );
    expect(mocks.invalidateBoardConfiguration).toHaveBeenCalledWith(1, null);
  });

  test('loads and saves columns for the collection scope', async () => {
    render(SupportQueue, { props: { workspaceId: 1, collectionId: 5 } });
    await screen.findByTestId('support-queue-tab-unassigned');

    await waitFor(() => expect(mocks.getBoardConfiguration).toHaveBeenCalledWith(5, 1));

    await fireEvent.click(screen.getByTestId('queue-column-selector-trigger'));
    await fireEvent.click(await screen.findByTestId('add-column-assignee'));
    await fireEvent.click(screen.getByTestId('column-selector-apply'));

    await waitFor(() => expect(mocks.createBoardConfiguration).toHaveBeenCalledTimes(1));
    const [collectionArg, workspaceArg] = mocks.createBoardConfiguration.mock.calls[0];
    expect(collectionArg).toBe(5);
    expect(workspaceArg).toBe(1);
    expect(mocks.invalidateBoardConfiguration).toHaveBeenCalledWith(1, 5);
  });

  test('renders the shared list column configuration, including queue labels', async () => {
    mocks.getBoardConfiguration.mockResolvedValue({
      id: 9,
      list_columns: [
        { field_identifier: 'key', field_type: 'system', display_order: 0, width: 1 },
        { field_identifier: 'title', field_type: 'system', display_order: 1, width: 4 },
        { field_identifier: 'assignee', field_type: 'system', display_order: 2, width: 2 },
        { field_identifier: 'updated_at', field_type: 'system', display_order: 3, width: 2 },
      ],
    });

    render(SupportQueue, { props: { workspaceId: 1 } });
    await screen.findByTestId('support-queue-row-11');

    expect(mocks.getBoardConfiguration).toHaveBeenCalledWith(null, 1);
    expect(screen.getByText('supportQueue.columnOwner')).toBeInTheDocument();
    expect(screen.getByText('supportQueue.columnUpdated')).toBeInTheDocument();
    expect(screen.queryByText('Created')).toBeNull();
  });

  test('bulk assignment routes the selected ids through the bulk endpoint', async () => {
    render(SupportQueue, { props: { workspaceId: 1 } });

    const checkbox = await screen.findByTestId('support-queue-item-checkbox-11');
    fireEvent.change(checkbox, { target: { checked: true } });
    await waitFor(() => expect(screen.getByTestId('support-queue-bulk-bar')).toBeVisible());

    fireEvent.click(screen.getByTestId('support-queue-bulk-assign-me'));

    await waitFor(() => expect(mocks.bulkUpdate).toHaveBeenCalledTimes(1));
    expect(mocks.bulkUpdate).toHaveBeenCalledWith([11], { assignee_id: 7 });
    await waitFor(() =>
      expect(mocks.fetchV2Data).toHaveBeenCalledWith(expect.stringContaining('/queues')),
    );
  });

  test('bulk team assignment picks through the searchable combobox and applies once', async () => {
    mocks.getAllTeams.mockResolvedValue([
      { id: 4, name: 'Support' },
      { id: 5, name: 'Sales' },
    ]);
    render(SupportQueue, { props: { workspaceId: 1 } });

    const checkbox = await screen.findByTestId('support-queue-item-checkbox-11');
    fireEvent.change(checkbox, { target: { checked: true } });
    await waitFor(() => expect(screen.getByTestId('support-queue-bulk-bar')).toBeVisible());

    // The team control is a searchable popover combobox, not a native select.
    await fireEvent.click(screen.getByTestId('team-picker-trigger'));
    const teamSearch = await screen.findByTestId('support-queue-bulk-team-search');
    await fireEvent.input(teamSearch, { target: { value: 'Sup' } });
    await fireEvent.click(await screen.findByTestId('team-picker-option-4'));
    await fireEvent.click(screen.getByTestId('support-queue-bulk-team-apply'));

    await waitFor(() => expect(mocks.bulkUpdate).toHaveBeenCalledTimes(1));
    expect(mocks.bulkUpdate).toHaveBeenCalledWith([11], { team_id: 4 });
  });

  test('bulk transition sends every selected id to the batch endpoint in one call', async () => {
    render(SupportQueue, { props: { workspaceId: 1 } });

    const checkbox = await screen.findByTestId('support-queue-item-checkbox-11');
    fireEvent.change(checkbox, { target: { checked: true } });
    await waitFor(() => expect(screen.getByTestId('support-queue-bulk-bar')).toBeVisible());

    await fireEvent.click(document.getElementById('support-queue-bulk-status'));
    await fireEvent.click(
      await waitFor(() => {
        const option = document.querySelector('[role="option"][data-option-id="2"]');
        if (!option) throw new Error('status option not rendered yet');
        return option;
      })
    );
    await fireEvent.click(screen.getByTestId('support-queue-bulk-status-apply'));

    await waitFor(() => expect(mocks.bulkTransition).toHaveBeenCalledTimes(1));
    expect(mocks.bulkTransition).toHaveBeenCalledWith([11], 2);
    await waitFor(() =>
      expect(mocks.fetchV2Data).toHaveBeenCalledWith(expect.stringContaining('/queues')),
    );
  });

  test('bulk delete confirms once and deletes every selected id in one call', async () => {
    render(SupportQueue, { props: { workspaceId: 1 } });

    const checkbox = await screen.findByTestId('support-queue-item-checkbox-11');
    fireEvent.change(checkbox, { target: { checked: true } });
    await waitFor(() => expect(screen.getByTestId('support-queue-bulk-bar')).toBeVisible());

    await fireEvent.click(screen.getByTestId('support-queue-bulk-delete'));

    await waitFor(() => expect(mocks.confirm).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(mocks.bulkDelete).toHaveBeenCalledTimes(1));
    expect(mocks.bulkDelete).toHaveBeenCalledWith([11]);
    await waitFor(() =>
      expect(mocks.fetchV2Data).toHaveBeenCalledWith(expect.stringContaining('/queues')),
    );
  });

  test('discards a superseded queue load when the tab changes (WI-1588)', async () => {
    const stale = deferred();
    let itemsCalls = 0;
    mocks.fetchV2Data.mockImplementation((url) => {
      if (url.includes('/queues')) return Promise.resolve(QUEUES);
      return Promise.resolve([]);
    });
    mocks.getAll.mockImplementation(() => {
      itemsCalls += 1;
      if (itemsCalls === 1) return stale.promise;
      return Promise.resolve(itemsResponse([{ ...ROWS[0], id: 99, title: 'Team-owned row' }]));
    });

    render(SupportQueue, { props: { workspaceId: 1 } });
    await screen.findByTestId('support-queue-tab-unassigned');

    // Switch away before the unassigned load resolves.
    fireEvent.click(screen.getByTestId('support-queue-tab-team-owned'));
    await waitFor(() => expect(screen.getByTestId('support-queue-row-99')).toBeVisible());

    // The late unassigned response must not replace the active queue's rows.
    stale.resolve(itemsResponse(ROWS));
    await Promise.resolve();
    await Promise.resolve();
    expect(screen.queryByTestId('support-queue-row-11')).toBeNull();
    expect(screen.getByTestId('support-queue-row-99')).toBeVisible();
  });

  test('search reloads the queue through the server-side search parameter', async () => {
    render(SupportQueue, { props: { workspaceId: 1 } });

    await screen.findByTestId('support-queue-row-11');
    const search = screen.getByPlaceholderText('common.search');
    fireEvent.input(search, { target: { value: 'attachment' } });

    await waitFor(() => expect(screen.queryByTestId('support-queue-row-11')).toBeNull());
    expect(screen.getByTestId('support-queue-row-12')).toBeVisible();
    expect(mocks.getAll).toHaveBeenLastCalledWith(
      expect.objectContaining({ search: 'attachment', page: 1 })
    );
  });

  test('paginates the active queue with the shared list pager', async () => {
    const firstPage = ROWS.map((row, index) => ({ ...row, id: 100 + index }));
    const secondPage = [{ ...ROWS[0], id: 200, title: 'Second page ticket' }];
    mocks.getAll.mockImplementation((filters = {}) => {
      const page = filters.page ?? 1;
      if (page === 1) {
        return Promise.resolve(itemsResponse(firstPage, { page: 1, limit: 2, totalItems: 3 }));
      }
      return Promise.resolve(itemsResponse(secondPage, { page: 2, limit: 2, totalItems: 3 }));
    });

    render(SupportQueue, { props: { workspaceId: 1 } });
    await screen.findByTestId('support-queue-row-100');

    // The shared pager replaces the old "showing the first N" note.
    await fireEvent.click(screen.getByTestId('pagination-next'));

    await waitFor(() => expect(screen.getByTestId('support-queue-row-200')).toBeVisible());
    expect(screen.queryByTestId('support-queue-row-100')).toBeNull();
    expect(mocks.getAll).toHaveBeenLastCalledWith(
      expect.objectContaining({ page: 2, limit: 2 })
    );
  });

  test('falls back to the unassigned preset when the deep-linked queue is gone', async () => {
    render(SupportQueue, { props: { workspaceId: 1, queue: 'deleted-queue' } });

    await waitFor(() =>
      expect(screen.getByTestId('support-queue-tab-unassigned')).toHaveAttribute(
        'aria-selected',
        'true',
      ),
    );
  });

  test('creates a queue through the editor and posts the raw QL', async () => {
    render(SupportQueue, { props: { workspaceId: 1 } });
    await screen.findByTestId('support-queue-tab-unassigned');

    await fireEvent.click(screen.getByTestId('support-queue-add'));
    await screen.findByTestId('support-queue-editor');
    await fireEvent.input(screen.getByTestId('queue-editor-name'), {
      target: { value: 'Escalations' },
    });
    await fireEvent.click(screen.getByTestId('queue-editor-builder-mode-raw'));
    await fireEvent.input(screen.getByTestId('queue-editor-builder-ql-editor'), {
      target: { value: 'priority = 1' },
    });
    await fireEvent.click(screen.getByTestId('queue-editor-save'));

    await waitFor(() =>
      expect(mocks.fetchV2Data).toHaveBeenCalledWith(
        '/workspaces/SQA/queues',
        expect.objectContaining({ method: 'POST' }),
      ),
    );
    const [, options] = mocks.fetchV2Data.mock.calls.find(
      ([url, opts]) => url === '/workspaces/SQA/queues' && opts?.method === 'POST',
    );
    expect(JSON.parse(options.body)).toEqual({
      name: 'Escalations',
      ql_query: 'priority = 1',
      filter_state: null,
    });
  });

  test('edits a custom queue with a merge-patch request', async () => {
    render(SupportQueue, { props: { workspaceId: 1 } });
    await screen.findByTestId('support-queue-tab-42');

    await fireEvent.click(screen.getByTestId('support-queue-tab-42'));
    await fireEvent.click(await screen.findByTestId('support-queue-menu-42'));
    await fireEvent.click(await screen.findByTestId('support-queue-edit'));

    const nameInput = await screen.findByTestId('queue-editor-name');
    expect(nameInput.value).toBe('My queue');
    await fireEvent.input(nameInput, { target: { value: 'Renamed queue' } });
    await fireEvent.click(screen.getByTestId('queue-editor-save'));

    await waitFor(() =>
      expect(mocks.fetchV2Data).toHaveBeenCalledWith(
        '/queues/42',
        expect.objectContaining({
          method: 'PATCH',
          headers: { 'Content-Type': 'application/merge-patch+json' },
        }),
      ),
    );
    const [, options] = mocks.fetchV2Data.mock.calls.find(
      ([url, opts]) => url === '/queues/42' && opts?.method === 'PATCH',
    );
    expect(JSON.parse(options.body)).toEqual({
      name: 'Renamed queue',
      ql_query: 'cf_priority = 3',
      filter_state: '{"dynamicFields":[]}',
    });
  });

  test('removes a built-in preset through the tab menu', async () => {
    render(SupportQueue, { props: { workspaceId: 1 } });
    await screen.findByTestId('support-queue-tab-unassigned');

    await fireEvent.click(screen.getByTestId('support-queue-menu-unassigned'));
    await fireEvent.click(await screen.findByTestId('support-queue-remove-builtin-unassigned'));

    await waitFor(() =>
      expect(mocks.fetchV2Data).toHaveBeenCalledWith(
        '/workspaces/SQA/queues/builtins/unassigned',
        expect.objectContaining({ method: 'PUT' }),
      ),
    );
  });

  test('deletes a custom queue after confirmation', async () => {
    render(SupportQueue, { props: { workspaceId: 1 } });
    await screen.findByTestId('support-queue-tab-42');

    await fireEvent.click(screen.getByTestId('support-queue-tab-42'));
    await fireEvent.click(await screen.findByTestId('support-queue-menu-42'));
    await fireEvent.click(await screen.findByTestId('support-queue-delete'));

    await waitFor(() => expect(mocks.confirm).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(mocks.fetchV2Data).toHaveBeenCalledWith(
        '/queues/42',
        expect.objectContaining({ method: 'DELETE' }),
      ),
    );
  });

  test('uses the collection-scoped API when rendered in a collection', async () => {
    render(SupportQueue, { props: { workspaceId: 1, collectionId: 5 } });

    await waitFor(() =>
      expect(mocks.fetchV2Data).toHaveBeenCalledWith('/collections/5/queues'),
    );
    await waitFor(() => expect(screen.getByTestId('support-queue-add')).toBeInTheDocument());
  });

  test('styles queue tabs with the tree glass treatment instead of gradient-only white text', async () => {
    render(SupportQueue, { props: { workspaceId: 1 } });
    const activeTab = await screen.findByTestId('support-queue-tab-unassigned');
    const activeTabStyle = activeTab.parentElement.getAttribute('style');

    // The glass surface keeps labels legible over custom backgrounds; the
    // active tab layers the design-system highlight on top.
    expect(activeTabStyle).toContain('background-color: var(--ds-glass-bg)');
    expect(activeTabStyle).toContain('color: var(--ds-text)');
    expect(activeTabStyle).toContain('--ctx-active-bg');
    expect(activeTabStyle).not.toContain('--ctx-active-text');

    const inactiveTab = screen.getByTestId('support-queue-tab-42');
    const inactiveTabStyle = inactiveTab.parentElement.getAttribute('style');
    expect(inactiveTabStyle).toContain('background-color: var(--ds-glass-bg)');
    expect(inactiveTabStyle).toContain('color: var(--ds-text)');
    expect(inactiveTabStyle).not.toContain('--ctx-active-bg');
  });

  test('hides queue management from callers who cannot write', async () => {
    mocks.canAdmin = false;
    render(SupportQueue, { props: { workspaceId: 1 } });
    await screen.findByTestId('support-queue-tab-unassigned');

    expect(screen.queryByTestId('support-queue-add')).toBeNull();
    expect(screen.queryByTestId('support-queue-menu-unassigned')).toBeNull();
    expect(screen.queryByTestId('queue-column-selector-trigger')).toBeNull();
  });
});

// Regression: deleting a ticket from the item detail modal emits no local
// event, so the queue has to reconcile through the same collection/workspace
// SSE stream the board and list views use (WI-1624).
describe('SupportQueue live updates', () => {
  let fakeEventSource;

  function installFakeEventSource() {
    const instances = [];
    class FakeEventSource {
      constructor(url) {
        this.url = url;
        this.listeners = new Map();
        instances.push(this);
      }
      addEventListener(type, callback) {
        if (!this.listeners.has(type)) this.listeners.set(type, []);
        this.listeners.get(type).push(callback);
      }
      emit(type) {
        for (const callback of this.listeners.get(type) ?? []) callback();
      }
      close() {}
    }
    globalThis.EventSource = FakeEventSource;
    return {
      instances,
      restore: () => {
        delete globalThis.EventSource;
      },
    };
  }

  beforeEach(() => {
    fakeEventSource = installFakeEventSource();
  });

  afterEach(() => {
    fakeEventSource.restore();
  });

  test('subscribes to the workspace stream and drops a deleted ticket on change', async () => {
    render(SupportQueue, { props: { workspaceId: 1 } });
    await screen.findByTestId('support-queue-row-11');

    expect(fakeEventSource.instances).toHaveLength(1);
    expect(fakeEventSource.instances[0].url).toContain('/api/workspaces/1/events');

    // The next snapshot after the delete no longer contains the removed row.
    mocks.fetchV2Data.mockImplementation((url) => {
      if (url.includes('/queues')) return Promise.resolve(QUEUES);
      return Promise.resolve([]);
    });
    mocks.getAll.mockImplementation(() => Promise.resolve(itemsResponse([ROWS[1]])));

    fakeEventSource.instances[0].emit('changed');

    await waitFor(() => expect(screen.queryByTestId('support-queue-row-11')).toBeNull());
    expect(screen.getByTestId('support-queue-row-12')).toBeVisible();
  });

  test('subscribes to the collection stream when rendered in a collection', async () => {
    render(SupportQueue, { props: { workspaceId: 1, collectionId: 5 } });
    await screen.findByTestId('support-queue-tab-unassigned');

    expect(fakeEventSource.instances).toHaveLength(1);
    expect(fakeEventSource.instances[0].url).toContain('/api/collections/5/events');
  });
});
