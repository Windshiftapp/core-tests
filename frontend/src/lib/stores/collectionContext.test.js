import { afterAll, afterEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  routeSubscriber: null,
  fetchCollectionBacklog: vi.fn(),
  fetchCollectionItemChanges: vi.fn(),
  fetchCollectionItems: vi.fn(),
  getBoardConfigurationBootstrap: vi.fn(),
}));

vi.mock('../api.js', () => ({
  api: {
    collections: {
      getBoardConfigurationBootstrap: mocks.getBoardConfigurationBootstrap,
    },
  },
}));

vi.mock('../features/collections/collectionService.js', () => ({
  fetchCollectionBacklog: mocks.fetchCollectionBacklog,
  fetchCollectionItemChanges: mocks.fetchCollectionItemChanges,
  fetchCollectionItems: mocks.fetchCollectionItems,
  fetchItemsById: vi.fn(),
  getCollection: vi.fn(),
}));

vi.mock('../router.js', () => ({
  currentRoute: {
    subscribe(callback) {
      mocks.routeSubscriber = callback;
      callback({ view: 'home', params: {} });
      return vi.fn();
    },
  },
  GLOBAL_COLLECTION_VIEWS: new Set(['collection-board', 'collection-list']),
}));

vi.mock('./workspaceDataStore.svelte.js', () => ({
  workspaceDataStore: {
    initialize: vi.fn(),
    initializeGlobal: vi.fn(),
    statuses: [],
  },
}));

const { collectionStore } = await import('./collectionContext.svelte.js');

function itemResult(options) {
  const page = options.page ?? 1;
  return {
    items: [{ id: page, status_id: 1 }],
    collectionName: 'Test board',
    pagination: { page, limit: options.limit, total: 2, total_pages: 2 },
    sortableFields: [],
    watermark: 1,
  };
}

describe('CollectionStore board ordering', () => {
  afterAll(() => collectionStore.destroy());
  afterEach(() => vi.restoreAllMocks());

  it('loads all unfinished board items before paging completed work', async () => {
    mocks.fetchCollectionItems.mockClear();
    mocks.fetchCollectionBacklog.mockResolvedValue({
      items: [],
      pagination: { page: 1, limit: 100, total: 0, total_pages: 0 },
      watermark: 4,
    });
    mocks.getBoardConfigurationBootstrap.mockResolvedValue({
      board_configuration: { columns: [], show_rightmost_column_last_50: false },
      statuses: [
        { id: 1, name: 'Open', category_name: 'To Do', is_completed: false },
        { id: 2, name: 'Done', category_name: 'Done', is_completed: true },
      ],
      referenced_workspace_ids: [41],
    });
    mocks.fetchCollectionItems.mockImplementation((_workspaceId, _collectionId, options) => {
      if (options.status_id_not === '2') {
        return Promise.resolve({
          items: Array.from({ length: 38 }, (_, index) => ({
            id: index + 1,
            status_id: 1,
          })),
          collectionName: 'Split board',
          pagination: { page: 1, limit: 1000, total: 38, total_pages: 1 },
          watermark: 4,
        });
      }
      if (options.status_id === '2') {
        const count = options.page === 1 ? 100 : 5;
        const start = options.page === 1 ? 1000 : 1100;
        return Promise.resolve({
          items: Array.from({ length: count }, (_, index) => ({
            id: start + index,
            status_id: 2,
          })),
          collectionName: 'Split board',
          pagination: { page: options.page, limit: 100, total: 105, total_pages: 2 },
          watermark: 4,
        });
      }
      throw new Error(`unexpected item request: ${JSON.stringify(options)}`);
    });

    mocks.routeSubscriber({ view: 'workspace-board', params: { id: '41' } });
    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));

    expect(mocks.fetchCollectionItems).toHaveBeenCalledTimes(2);
    expect(collectionStore.items.filter((item) => item.status_id === 1)).toHaveLength(38);
    expect(collectionStore.items.filter((item) => item.status_id === 2)).toHaveLength(100);
    expect(collectionStore.itemsTotalCount).toBe(143);
    expect(collectionStore.itemsRemainingCount).toBe(5);
    expect(collectionStore.itemsHasMore).toBe(true);

    await collectionStore.loadMoreItems();

    expect(mocks.fetchCollectionItems).toHaveBeenLastCalledWith(
      '41',
      null,
      expect.objectContaining({ page: 2, limit: 100, status_id: '2' })
    );
    expect(collectionStore.items.filter((item) => item.status_id === 2)).toHaveLength(105);
    expect(collectionStore.itemsRemainingCount).toBe(0);
    expect(collectionStore.itemsHasMore).toBe(false);
  });

  it('sends one completed-activity day window to initial and later completed pages', async () => {
    mocks.fetchCollectionItems.mockClear();
    mocks.fetchCollectionBacklog.mockResolvedValue({
      items: [],
      pagination: { page: 1, limit: 100, total: 0, total_pages: 0 },
      watermark: 5,
    });
    mocks.getBoardConfigurationBootstrap.mockResolvedValue({
      board_configuration: {
        columns: [],
        show_rightmost_column_last_50: false,
        completed_item_retention_days: 30,
      },
      statuses: [
        { id: 1, name: 'Open', category_name: 'To Do', is_completed: false },
        { id: 2, name: 'Done', category_name: 'Done', is_completed: true },
      ],
      referenced_workspace_ids: [47],
    });
    mocks.fetchCollectionItems.mockImplementation((_workspaceId, _collectionId, options) => {
      if (options.status_id_not === '2') {
        return Promise.resolve({
          items: [{ id: 1, status_id: 1 }],
          collectionName: 'Age-trimmed board',
          pagination: { page: 1, limit: 1000, total: 1, total_pages: 1 },
          watermark: 5,
        });
      }
      if (options.status_id === '2') {
        return Promise.resolve({
          items: [{ id: 100 + options.page, status_id: 2 }],
          collectionName: 'Age-trimmed board',
          pagination: { page: options.page, limit: 100, total: 2, total_pages: 2 },
          watermark: 5,
        });
      }
      throw new Error(`unexpected item request: ${JSON.stringify(options)}`);
    });

    mocks.routeSubscriber({ view: 'workspace-board', params: { id: '47' } });
    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));

    expect(mocks.fetchCollectionItems).toHaveBeenCalledWith(
      '47',
      null,
      expect.objectContaining({
        page: 1,
        status_id: '2',
        completed_activity_days: 30,
      })
    );

    await collectionStore.loadMoreItems();
    expect(mocks.fetchCollectionItems).toHaveBeenLastCalledWith(
      '47',
      null,
      expect.objectContaining({
        page: 2,
        status_id: '2',
        completed_activity_days: 30,
      })
    );
  });

  it('searches the complete collection scope without board trimming filters', async () => {
    mocks.fetchCollectionItems.mockClear();
    mocks.fetchCollectionBacklog.mockResolvedValue({
      items: [],
      pagination: { page: 1, limit: 100, total: 0, total_pages: 0 },
      watermark: 6,
    });
    const collection = { id: 88, name: 'Scoped search collection' };
    mocks.getBoardConfigurationBootstrap.mockResolvedValue({
      board_configuration: {
        columns: [
          { id: 1, status_ids: [1] },
          { id: 2, status_ids: [2] },
        ],
        show_rightmost_column_last_50: true,
      },
      collection,
      statuses: [
        { id: 1, name: 'Open', category_name: 'To Do', is_completed: false },
        { id: 2, name: 'Done', category_name: 'Done', is_completed: true },
      ],
      referenced_workspace_ids: [51],
    });
    mocks.fetchCollectionItems.mockImplementation((_workspaceId, _collectionId, options) => {
      if (options.search) {
        return Promise.resolve({
          items: [{ id: 900 + options.page, status_id: 2 }],
          collectionName: collection.name,
          pagination: { page: options.page, limit: 100, total: 2, total_pages: 2 },
          watermark: 6,
        });
      }
      if (options.status_id_not === '2') {
        return Promise.resolve({
          items: [{ id: 1, status_id: 1 }],
          collectionName: collection.name,
          pagination: { page: 1, limit: 1000, total: 1, total_pages: 1 },
          watermark: 6,
        });
      }
      if (options.status_id === '2') {
        return Promise.resolve({
          items: [{ id: 2, status_id: 2 }],
          collectionName: collection.name,
          pagination: { page: 1, limit: 50, total: 55, total_pages: 2 },
          watermark: 6,
        });
      }
      throw new Error(`unexpected item request: ${JSON.stringify(options)}`);
    });

    mocks.routeSubscriber({ view: 'collection-board', params: { id: '88' } });
    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));
    collectionStore.subFilterQL = 'status = "Done"';

    await collectionStore.searchBoardItems('description needle');

    const searchCall = mocks.fetchCollectionItems.mock.calls.find(
      ([, , options]) => options.search === 'description needle'
    );
    expect(searchCall).toBeDefined();
    expect(searchCall[0]).toBeNull();
    expect(searchCall[1]).toBe('88');
    expect(searchCall[2]).toEqual(
      expect.objectContaining({
        page: 1,
        limit: 100,
        search: 'description needle',
        sub_ql: 'status = "Done"',
        collection,
      })
    );
    expect(searchCall[2]).not.toHaveProperty('status_id');
    expect(searchCall[2]).not.toHaveProperty('status_id_not');
    expect(searchCall[2]).not.toHaveProperty('completed_activity_days');
    expect(collectionStore.boardSearchItems).toEqual([{ id: 901, status_id: 2 }]);
    expect(collectionStore.boardSearchRemainingCount).toBe(1);
    expect(collectionStore.boardSearchHasMore).toBe(true);

    await collectionStore.loadMoreBoardSearchItems();
    expect(collectionStore.boardSearchItems).toEqual([
      { id: 901, status_id: 2 },
      { id: 902, status_id: 2 },
    ]);
    expect(collectionStore.boardSearchRemainingCount).toBe(0);
    expect(collectionStore.boardSearchHasMore).toBe(false);
  });

  it('applies Bubble Mode before pagination on loads, refreshes, and later pages', async () => {
    mocks.fetchCollectionItems.mockImplementation((_workspaceId, _collectionId, options) =>
      Promise.resolve(itemResult(options))
    );
    mocks.fetchCollectionBacklog.mockResolvedValue({
      items: [],
      pagination: { page: 1, limit: 100, total: 0, total_pages: 0 },
    });
    mocks.fetchCollectionItemChanges.mockResolvedValue({ watermark: 1 });
    mocks.getBoardConfigurationBootstrap.mockResolvedValue({ board_configuration: null });

    mocks.routeSubscriber({ view: 'workspace-board', params: { id: '42' } });
    await vi.waitFor(() => expect(mocks.fetchCollectionItems).toHaveBeenCalledTimes(1));
    expect(mocks.fetchCollectionItems).toHaveBeenLastCalledWith(
      '42',
      null,
      expect.objectContaining({
        page: 1,
        limit: 100,
        order_by: 'frac_index',
        sort_direction: 'asc',
      })
    );

    mocks.fetchCollectionItems.mockClear();
    collectionStore.setBoardSortMode('bubble');
    await vi.waitFor(() => expect(mocks.fetchCollectionItems).toHaveBeenCalledTimes(1));
    expect(mocks.fetchCollectionItems).toHaveBeenLastCalledWith(
      '42',
      null,
      expect.objectContaining({
        page: 1,
        order_by: 'last_active_at',
        sort_direction: 'desc',
      })
    );

    mocks.fetchCollectionItems.mockClear();
    await collectionStore.loadMoreItems();
    expect(mocks.fetchCollectionItems).toHaveBeenCalledWith(
      '42',
      null,
      expect.objectContaining({
        page: 2,
        order_by: 'last_active_at',
        sort_direction: 'desc',
      })
    );

    mocks.fetchCollectionItems.mockClear();
    await collectionStore.refresh();
    expect(mocks.fetchCollectionItems).toHaveBeenCalledWith(
      '42',
      null,
      expect.objectContaining({
        page: 1,
        order_by: 'last_active_at',
        sort_direction: 'desc',
      })
    );

    mocks.fetchCollectionItems.mockClear();
    mocks.routeSubscriber({ view: 'workspace-board', params: { id: '43' } });
    // A workspace change must synchronously hide the previous board while the
    // new request is in flight. MainApp reuses the mounted board component.
    expect(collectionStore.items).toEqual([]);
    expect(collectionStore.backlogItems).toEqual([]);
    expect(collectionStore.itemsPagination).toBeNull();
    await vi.waitFor(() => expect(mocks.fetchCollectionItems).toHaveBeenCalledTimes(1));
    expect(mocks.fetchCollectionItems).toHaveBeenLastCalledWith(
      '43',
      null,
      expect.objectContaining({
        page: 1,
        order_by: 'last_active_at',
        sort_direction: 'desc',
      })
    );

    mocks.fetchCollectionItems.mockClear();
    collectionStore.setBoardSortMode('rank');
    await vi.waitFor(() => expect(mocks.fetchCollectionItems).toHaveBeenCalledTimes(1));
    expect(mocks.fetchCollectionItems).toHaveBeenLastCalledWith(
      '43',
      null,
      expect.objectContaining({
        page: 1,
        order_by: 'frac_index',
        sort_direction: 'asc',
      })
    );
  });

  it('does not log expected connectivity failures from delta polling', async () => {
    mocks.fetchCollectionItems.mockImplementation((_workspaceId, _collectionId, options) =>
      Promise.resolve(itemResult(options))
    );
    mocks.fetchCollectionBacklog.mockResolvedValue({
      items: [],
      pagination: { page: 1, limit: 100, total: 0, total_pages: 0 },
    });
    mocks.getBoardConfigurationBootstrap.mockResolvedValue({ board_configuration: null });

    mocks.routeSubscriber({ view: 'workspace-list', params: { id: '44' } });
    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));

    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    mocks.fetchCollectionItemChanges.mockRejectedValueOnce(
      Object.assign(new Error('offline'), { code: 'NETWORK_ERROR' })
    );

    await collectionStore.refreshDeltas();

    expect(errorSpy).not.toHaveBeenCalled();
  });

  it('loads only items for list views and only backlog for backlog views', async () => {
    mocks.fetchCollectionItems.mockClear();
    mocks.fetchCollectionBacklog.mockClear();
    mocks.fetchCollectionItemChanges.mockClear();
    mocks.fetchCollectionItems.mockImplementation((_workspaceId, _collectionId, options) =>
      Promise.resolve(itemResult(options))
    );
    mocks.fetchCollectionBacklog.mockResolvedValue({
      items: [{ id: 2, status_id: 1 }],
      collectionName: 'Backlog',
      pagination: { page: 1, limit: 100, total: 1, total_pages: 1 },
      watermark: 3,
    });

    mocks.routeSubscriber({ view: 'workspace-list', params: { id: '45' } });
    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));

    expect(mocks.fetchCollectionItems).toHaveBeenCalledTimes(1);
    expect(mocks.fetchCollectionBacklog).not.toHaveBeenCalled();
    expect(mocks.fetchCollectionItemChanges).not.toHaveBeenCalled();

    mocks.fetchCollectionItems.mockClear();
    mocks.fetchCollectionBacklog.mockClear();
    mocks.routeSubscriber({ view: 'workspace-backlog', params: { id: '46' } });
    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));

    expect(mocks.fetchCollectionItems).not.toHaveBeenCalled();
    expect(mocks.fetchCollectionBacklog).toHaveBeenCalledTimes(1);
  });

  it('polls from the oldest watermark returned by parallel board snapshots', async () => {
    mocks.fetchCollectionItems.mockImplementation((_workspaceId, _collectionId, options) =>
      Promise.resolve({ ...itemResult(options), watermark: 5 })
    );
    mocks.fetchCollectionBacklog.mockResolvedValue({
      items: [],
      pagination: { page: 1, limit: 100, total: 0, total_pages: 0 },
      watermark: 7,
    });
    mocks.fetchCollectionItemChanges.mockResolvedValue({
      watermark: 7,
      changed_item_ids: [],
      removed_item_ids: [],
    });
    mocks.getBoardConfigurationBootstrap.mockResolvedValue({ board_configuration: null });

    mocks.routeSubscriber({ view: 'workspace-board', params: { id: '47' } });
    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));
    await collectionStore.refreshDeltas();

    expect(mocks.fetchCollectionItemChanges).toHaveBeenLastCalledWith(
      '47',
      null,
      expect.objectContaining({ since: 5 })
    );
  });

  it('single-flights board configuration for store and view consumers', async () => {
    mocks.getBoardConfigurationBootstrap.mockClear();
    let resolveConfiguration;
    mocks.getBoardConfigurationBootstrap.mockReturnValue(
      new Promise((resolve) => {
        resolveConfiguration = resolve;
      })
    );

    const storeLoad = collectionStore.getBoardConfiguration(48, null, { force: true });
    const viewLoad = collectionStore.getBoardConfiguration(48, null);

    expect(mocks.getBoardConfigurationBootstrap).toHaveBeenCalledTimes(1);
    resolveConfiguration({
      board_configuration: { id: 9, columns: [] },
      collection: { id: 7, ql_query: 'workspace_id = 48' },
      referenced_workspace_ids: [48],
    });
    await Promise.all([storeLoad, viewLoad]);
    expect(collectionStore.boardWorkspaceScopeLoaded).toBe(true);
    expect(collectionStore.boardWorkspaceIds).toEqual([48]);
    expect(collectionStore.boardCollection).toEqual({ id: 7, ql_query: 'workspace_id = 48' });
  });
});
