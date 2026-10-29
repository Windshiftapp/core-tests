import { afterAll, afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { collections } from '../api/collections.js';

const mocks = vi.hoisted(() => ({
  routeSubscriber: null,
  fetchCollectionBacklog: vi.fn(),
  fetchCollectionItemChanges: vi.fn(),
  fetchCollectionItems: vi.fn(),
  fetchCollectionTotal: vi.fn().mockResolvedValue({ total: 300, watermark: 100 }),
  getBoardConfigurationBootstrap: vi.fn(),
  getCollection: vi.fn(),
}));

vi.mock('../api/collections.js', () => ({
  collections: {
        getBoardConfigurationBootstrap: mocks.getBoardConfigurationBootstrap,
      },
  collectionCategories: { getAll: vi.fn().mockResolvedValue([]) },
}));

vi.mock('../features/collections/collectionService.js', () => ({
  fetchCollectionTotal: mocks.fetchCollectionTotal,
  fetchCollectionBacklog: mocks.fetchCollectionBacklog,
  fetchCollectionItemChanges: mocks.fetchCollectionItemChanges,
  fetchCollectionItems: mocks.fetchCollectionItems,
  fetchItemsById: vi.fn(),
  getCollection: mocks.getCollection,
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
    pagination: { page, page_size: options.limit, total_items: 2, total_pages: 2 },
    sortableFields: [],
    watermark: 1,
  };
}

describe('CollectionStore board ordering', () => {
  beforeEach(() => {
    // These partition and retention tests explicitly include completed work.
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation((key) =>
      key.startsWith('collection-show-completed:') ? 'true' : null
    );
  });
  afterAll(() => collectionStore.destroy());
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('renders the first unfinished page without waiting for background pages', async () => {
    let resolveSecondPage;
    mocks.fetchCollectionItems.mockImplementation((_workspaceId, _collectionId, options) => {
      if (options.status_id === '2') {
        return Promise.resolve({
          items: [],
          pagination: { page: 1, page_size: 100, total_items: 0, total_pages: 0 },
          watermark: 1,
        });
      }
      if (options.page === 1) {
        return Promise.resolve({
          items: [{ id: 1, status_id: 1 }],
          collectionName: 'Large board',
          pagination: { page: 1, page_size: 100, total_items: 200, total_pages: 2 },
          watermark: 1,
        });
      }
      return new Promise((resolve) => {
        resolveSecondPage = resolve;
      });
    });
    mocks.fetchCollectionBacklog.mockResolvedValue({
      items: [],
      pagination: { page: 1, page_size: 100, total_items: 0, total_pages: 0 },
      watermark: 1,
    });
    mocks.getBoardConfigurationBootstrap.mockResolvedValue({
      board_configuration: { columns: [], show_rightmost_column_last_50: false },
      statuses: [{ id: 1, category_name: 'To Do', is_completed: false }],
      referenced_workspace_ids: [60],
    });

    mocks.routeSubscriber({ view: 'workspace-board', params: { id: '60' } });

    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));
    expect(collectionStore.items).toContainEqual({ id: 1, status_id: 1 });
    expect(resolveSecondPage).toBeTypeOf('function');

    resolveSecondPage({
      items: [{ id: 2, status_id: 1 }],
      collectionName: 'Large board',
      pagination: { page: 2, page_size: 100, total_items: 200, total_pages: 2 },
      watermark: 1,
    });
    await vi.waitFor(() => expect(collectionStore.items).toHaveLength(2));
  });

  it('paces unfinished board pages after the first thousand items', async () => {
    vi.useFakeTimers();
    mocks.fetchCollectionItems.mockImplementation((_workspaceId, _collectionId, options) => {
      if (options.status_id === '2') {
        return Promise.resolve({
          items: [],
          pagination: { page: 1, page_size: 100, total_items: 0, total_pages: 0 },
          watermark: 1,
        });
      }
      return Promise.resolve({
        items: Array.from({ length: 100 }, (_, index) => ({
          id: (options.page - 1) * 100 + index + 1,
          status_id: 1,
        })),
        collectionName: 'Very large board',
        pagination: { page: options.page, page_size: 100, total_items: 1100, total_pages: 11 },
        watermark: 1,
      });
    });
    mocks.fetchCollectionBacklog.mockResolvedValue({
      items: [],
      pagination: { page: 1, page_size: 100, total_items: 0, total_pages: 0 },
      watermark: 1,
    });
    mocks.getBoardConfigurationBootstrap.mockResolvedValue({
      board_configuration: { columns: [], show_rightmost_column_last_50: false },
      statuses: [
        { id: 1, category_name: 'To Do', is_completed: false },
        { id: 2, category_name: 'Done', is_completed: true },
      ],
      referenced_workspace_ids: [61],
    });

    mocks.routeSubscriber({ view: 'workspace-board', params: { id: '61' } });
    await vi.advanceTimersByTimeAsync(0);

    const unfinishedCalls = () =>
      mocks.fetchCollectionItems.mock.calls.filter(
        ([, , options]) => options.status_id_not === '2'
      );
    expect(collectionStore.loading).toBe(false);
    expect(unfinishedCalls()).toHaveLength(10);
    expect(collectionStore.items).toHaveLength(1000);

    await vi.advanceTimersByTimeAsync(249);
    expect(unfinishedCalls()).toHaveLength(10);
    await vi.advanceTimersByTimeAsync(1);
    expect(unfinishedCalls()).toHaveLength(11);
    await vi.waitFor(() => expect(collectionStore.items).toHaveLength(1100));
  });

  it('reuses the collection returned by the board bootstrap', async () => {
    const collection = { id: 62, name: 'Aggregate board', ql_query: 'workspace_id = 7' };
    mocks.getBoardConfigurationBootstrap.mockResolvedValue({
      board_configuration: { columns: [] },
      collection,
      statuses: [],
      referenced_workspace_ids: [7],
    });
    mocks.fetchCollectionItems.mockResolvedValue({
      items: [],
      collectionName: collection.name,
      pagination: { page: 1, page_size: 100, total_items: 0, total_pages: 0 },
      watermark: 1,
    });
    mocks.fetchCollectionBacklog.mockResolvedValue({
      items: [],
      pagination: { page: 1, page_size: 100, total_items: 0, total_pages: 0 },
      watermark: 1,
    });

    mocks.routeSubscriber({ view: 'collection-board', params: { id: '62' } });
    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));

    expect(mocks.getCollection).not.toHaveBeenCalled();
    expect(mocks.fetchCollectionItems).toHaveBeenCalledWith(
      null,
      '62',
      expect.objectContaining({ collection })
    );
  });

  it('loads all unfinished board items before paging completed work', async () => {
    mocks.fetchCollectionItems.mockClear();
    mocks.fetchCollectionBacklog.mockResolvedValue({
      items: [],
      pagination: { page: 1, page_size: 100, total_items: 0, total_pages: 0 },
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
          pagination: { page: 1, page_size: 1000, total_items: 38, total_pages: 1 },
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
          pagination: {
            page: options.page,
            page_size: 100,
            total_items: 105,
            total_pages: 2,
          },
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

  it('keeps load-more completed rows across a background refresh', async () => {
    mocks.fetchCollectionItems.mockClear();
    mocks.fetchCollectionBacklog.mockResolvedValue({
      items: [],
      pagination: { page: 1, page_size: 100, total_items: 0, total_pages: 0 },
      watermark: 4,
    });
    mocks.getBoardConfigurationBootstrap.mockResolvedValue({
      board_configuration: { columns: [], show_rightmost_column_last_50: false },
      statuses: [
        { id: 1, name: 'Open', category_name: 'To Do', is_completed: false },
        { id: 2, name: 'Done', category_name: 'Done', is_completed: true },
      ],
      referenced_workspace_ids: [63],
    });
    const deferredPage = (page, count) => ({
      items: Array.from({ length: count }, (_, index) => ({
        id: 1000 + (page - 1) * 100 + index,
        status_id: 2,
      })),
      collectionName: 'Deep completed board',
      pagination: {
        page,
        page_size: count,
        total_items: 250,
        total_pages: Math.ceil(250 / count),
      },
      watermark: 4,
    });
    mocks.fetchCollectionItems.mockImplementation((_workspaceId, _collectionId, options) => {
      if (options.status_id_not === '2') {
        return Promise.resolve({
          items: [{ id: 1, status_id: 1 }],
          collectionName: 'Deep completed board',
          pagination: { page: 1, page_size: 1000, total_items: 1, total_pages: 1 },
          watermark: 4,
        });
      }
      if (options.status_id === '2') {
        if (options.page === 1 && options.limit === 100) return deferredPage(1, 100);
        if (options.page === 2 && options.limit === 100) return deferredPage(2, 100);
        if (options.page === 1 && options.limit === 200) return deferredPage(1, 200);
      }
      throw new Error(`unexpected item request: ${JSON.stringify(options)}`);
    });

    mocks.routeSubscriber({ view: 'workspace-board', params: { id: '63' } });
    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));
    expect(collectionStore.items.filter((item) => item.status_id === 2)).toHaveLength(100);

    await collectionStore.loadMoreItems();
    expect(collectionStore.items.filter((item) => item.status_id === 2)).toHaveLength(200);

    mocks.fetchCollectionItems.mockClear();
    await collectionStore.refresh();

    expect(mocks.fetchCollectionItems).toHaveBeenCalledWith(
      '63',
      null,
      expect.objectContaining({ page: 1, limit: 200, status_id: '2' })
    );
    expect(collectionStore.items.filter((item) => item.status_id === 2)).toHaveLength(200);
    expect(collectionStore.itemsRemainingCount).toBe(50);
    expect(collectionStore.itemsHasMore).toBe(true);
  });

  it('sends one completed-activity day window to initial and later completed pages', async () => {
    mocks.fetchCollectionItems.mockClear();
    mocks.fetchCollectionBacklog.mockResolvedValue({
      items: [],
      pagination: { page: 1, page_size: 100, total_items: 0, total_pages: 0 },
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
          pagination: { page: 1, page_size: 1000, total_items: 1, total_pages: 1 },
          watermark: 5,
        });
      }
      if (options.status_id === '2') {
        return Promise.resolve({
          items: [{ id: 100 + options.page, status_id: 2 }],
          collectionName: 'Age-trimmed board',
          pagination: {
            page: options.page,
            page_size: 100,
            total_items: 2,
            total_pages: 2,
          },
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

  it('hides completed work entirely for the none preset', async () => {
    mocks.fetchCollectionItems.mockClear();
    mocks.fetchCollectionBacklog.mockResolvedValue({
      items: [],
      pagination: { page: 1, page_size: 100, total_items: 0, total_pages: 0 },
      watermark: 7,
    });
    mocks.getBoardConfigurationBootstrap.mockResolvedValue({
      board_configuration: {
        columns: [],
        show_rightmost_column_last_50: false,
        view_settings: { completed_visibility: 'none' },
      },
      statuses: [
        { id: 1, name: 'Open', category_name: 'To Do', is_completed: false },
        { id: 2, name: 'Done', category_name: 'Done', is_completed: true },
      ],
      referenced_workspace_ids: [71],
    });
    mocks.fetchCollectionItems.mockImplementation((_workspaceId, _collectionId, options) => {
      if (options.status_id_not === '2') {
        return Promise.resolve({
          items: [{ id: 1, status_id: 1 }],
          collectionName: 'No completed',
          pagination: { page: 1, page_size: 1000, total_items: 1, total_pages: 1 },
          watermark: 7,
        });
      }
      throw new Error(`unexpected item request: ${JSON.stringify(options)}`);
    });

    mocks.routeSubscriber({ view: 'workspace-board', params: { id: '71' } });
    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));

    expect(mocks.fetchCollectionItems).toHaveBeenCalledTimes(1);
    expect(mocks.fetchCollectionItems).toHaveBeenCalledWith(
      '71',
      null,
      expect.objectContaining({ status_id_not: '2' })
    );
    expect(collectionStore.boardDeferred).toBeNull();
    expect(collectionStore.items).toEqual([{ id: 1, status_id: 1 }]);
  });

  it('applies the last_week completed preset as a seven-day window', async () => {
    mocks.fetchCollectionItems.mockClear();
    mocks.fetchCollectionBacklog.mockResolvedValue({
      items: [],
      pagination: { page: 1, page_size: 100, total_items: 0, total_pages: 0 },
      watermark: 7,
    });
    mocks.getBoardConfigurationBootstrap.mockResolvedValue({
      board_configuration: {
        columns: [],
        show_rightmost_column_last_50: false,
        view_settings: { completed_visibility: 'last_week' },
      },
      statuses: [
        { id: 1, name: 'Open', category_name: 'To Do', is_completed: false },
        { id: 2, name: 'Done', category_name: 'Done', is_completed: true },
      ],
      referenced_workspace_ids: [72],
    });
    mocks.fetchCollectionItems.mockImplementation((_workspaceId, _collectionId, options) => {
      if (options.status_id_not === '2') {
        return Promise.resolve({
          items: [{ id: 1, status_id: 1 }],
          collectionName: 'Week window',
          pagination: { page: 1, page_size: 1000, total_items: 1, total_pages: 1 },
          watermark: 7,
        });
      }
      if (options.status_id === '2') {
        return Promise.resolve({
          items: [{ id: 2, status_id: 2 }],
          collectionName: 'Week window',
          pagination: { page: 1, page_size: 100, total_items: 1, total_pages: 1 },
          watermark: 7,
        });
      }
      throw new Error(`unexpected item request: ${JSON.stringify(options)}`);
    });

    mocks.routeSubscriber({ view: 'workspace-board', params: { id: '72' } });
    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));

    expect(mocks.fetchCollectionItems).toHaveBeenCalledWith(
      '72',
      null,
      expect.objectContaining({ status_id: '2', completed_activity_days: 7 })
    );
  });

  it('scopes the current_iteration preset to the active iteration', async () => {
    mocks.fetchCollectionItems.mockClear();
    mocks.fetchCollectionBacklog.mockResolvedValue({
      items: [],
      pagination: { page: 1, page_size: 100, total_items: 0, total_pages: 0 },
      watermark: 7,
    });
    mocks.getBoardConfigurationBootstrap.mockResolvedValue({
      board_configuration: {
        columns: [],
        show_rightmost_column_last_50: false,
        view_settings: { completed_visibility: 'current_iteration' },
      },
      statuses: [
        { id: 1, name: 'Open', category_name: 'To Do', is_completed: false },
        { id: 2, name: 'Done', category_name: 'Done', is_completed: true },
      ],
      referenced_workspace_ids: [73],
    });
    mocks.fetchCollectionItems.mockImplementation((_workspaceId, _collectionId, options) => {
      if (options.status_id_not === '2') {
        return Promise.resolve({
          items: [{ id: 1, status_id: 1 }],
          collectionName: 'Iteration window',
          pagination: { page: 1, page_size: 1000, total_items: 1, total_pages: 1 },
          watermark: 7,
        });
      }
      if (options.status_id === '2') {
        return Promise.resolve({
          items: [{ id: 2, status_id: 2 }],
          collectionName: 'Iteration window',
          pagination: { page: 1, page_size: 100, total_items: 1, total_pages: 1 },
          watermark: 7,
        });
      }
      throw new Error(`unexpected item request: ${JSON.stringify(options)}`);
    });

    mocks.routeSubscriber({ view: 'workspace-board', params: { id: '73' } });
    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));
    mocks.fetchCollectionItems.mockClear();

    collectionStore.setBoardCompletedIteration(55);

    await vi.waitFor(() =>
      expect(mocks.fetchCollectionItems).toHaveBeenCalledWith(
        '73',
        null,
        expect.objectContaining({ status_id: '2', iteration_id: 55 })
      )
    );
  });

  it('searches the complete collection scope without board trimming filters', async () => {
    mocks.fetchCollectionItems.mockClear();
    mocks.fetchCollectionBacklog.mockResolvedValue({
      items: [],
      pagination: { page: 1, page_size: 100, total_items: 0, total_pages: 0 },
      watermark: 6,
    });
    const collection = { id: 88, name: 'Scoped search collection' };
    mocks.getBoardConfigurationBootstrap.mockResolvedValue({
      board_configuration: {
        columns: [
          { id: 1, status_ids: [1] },
          { id: 2, status_ids: [2] },
        ],
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
          pagination: {
            page: options.page,
            page_size: 100,
            total_items: 2,
            total_pages: 2,
          },
          watermark: 6,
        });
      }
      if (options.status_id_not === '2') {
        return Promise.resolve({
          items: [{ id: 1, status_id: 1 }],
          collectionName: collection.name,
          pagination: { page: 1, page_size: 1000, total_items: 1, total_pages: 1 },
          watermark: 6,
        });
      }
      if (options.status_id === '2') {
        return Promise.resolve({
          items: [{ id: 2, status_id: 2 }],
          collectionName: collection.name,
          pagination: { page: 1, page_size: 50, total_items: 55, total_pages: 2 },
          watermark: 6,
        });
      }
      throw new Error(`unexpected item request: ${JSON.stringify(options)}`);
    });

    mocks.routeSubscriber({ view: 'collection-board', params: { id: '88' } });
    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));
    collectionStore.subFilterQL = 'status = "Done"';

    await collectionStore.runSearch('description needle');

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
    expect(collectionStore.searchItems).toEqual([{ id: 901, status_id: 2 }]);
    expect(collectionStore.searchRemainingCount).toBe(1);
    expect(collectionStore.searchHasMore).toBe(true);

    await collectionStore.loadMoreSearchItems();
    expect(collectionStore.searchItems).toEqual([
      { id: 901, status_id: 2 },
      { id: 902, status_id: 2 },
    ]);
    expect(collectionStore.searchRemainingCount).toBe(0);
    expect(collectionStore.searchHasMore).toBe(false);
  });

  it('applies Bubble Mode before pagination on loads, refreshes, and later pages', async () => {
    mocks.fetchCollectionItems.mockImplementation((_workspaceId, _collectionId, options) =>
      Promise.resolve(itemResult(options))
    );
    mocks.fetchCollectionBacklog.mockResolvedValue({
      items: [],
      pagination: { page: 1, page_size: 100, total_items: 0, total_pages: 0 },
    });
    mocks.fetchCollectionItemChanges.mockResolvedValue({ watermark: 1 });
    mocks.getBoardConfigurationBootstrap.mockResolvedValue({ board_configuration: null });

    mocks.routeSubscriber({ view: 'workspace-board', params: { id: '42' } });
    await vi.waitFor(() => expect(mocks.fetchCollectionItems).toHaveBeenCalledTimes(2));
    expect(mocks.fetchCollectionItems.mock.calls).toEqual(
      expect.arrayContaining([
        [
          '42',
          null,
          expect.objectContaining({
            page: 1,
            limit: 1000,
            order_by: 'frac_index',
            sort_direction: 'asc',
          }),
        ],
        [
          '42',
          null,
          expect.objectContaining({
            page: 2,
            limit: 1000,
            order_by: 'frac_index',
            sort_direction: 'asc',
          }),
        ],
      ])
    );

    mocks.fetchCollectionItems.mockClear();
    collectionStore.setBoardSortMode('bubble');
    await vi.waitFor(() => expect(mocks.fetchCollectionItems).toHaveBeenCalledTimes(2));
    for (const [, , options] of mocks.fetchCollectionItems.mock.calls) {
      expect(options).toEqual(
        expect.objectContaining({ order_by: 'last_active_at', sort_direction: 'desc' })
      );
    }

    mocks.fetchCollectionItems.mockClear();
    await collectionStore.loadMoreItems();
    expect(mocks.fetchCollectionItems).not.toHaveBeenCalled();

    mocks.fetchCollectionItems.mockClear();
    await collectionStore.refresh();
    expect(mocks.fetchCollectionItems).toHaveBeenCalledTimes(2);
    for (const [, , options] of mocks.fetchCollectionItems.mock.calls) {
      expect(options).toEqual(
        expect.objectContaining({ order_by: 'last_active_at', sort_direction: 'desc' })
      );
    }

    mocks.fetchCollectionItems.mockClear();
    mocks.routeSubscriber({ view: 'workspace-board', params: { id: '43' } });
    // A workspace change must synchronously hide the previous board while the
    // new request is in flight. MainApp reuses the mounted board component.
    expect(collectionStore.items).toEqual([]);
    expect(collectionStore.backlogItems).toEqual([]);
    expect(collectionStore.itemsPagination).toBeNull();
    await vi.waitFor(() => expect(mocks.fetchCollectionItems).toHaveBeenCalledTimes(2));
    for (const [workspaceId, , options] of mocks.fetchCollectionItems.mock.calls) {
      expect(workspaceId).toBe('43');
      expect(options).toEqual(
        expect.objectContaining({ order_by: 'last_active_at', sort_direction: 'desc' })
      );
    }

    mocks.fetchCollectionItems.mockClear();
    collectionStore.setBoardSortMode('rank');
    await vi.waitFor(() => expect(mocks.fetchCollectionItems).toHaveBeenCalledTimes(2));
    for (const [, , options] of mocks.fetchCollectionItems.mock.calls) {
      expect(options).toEqual(
        expect.objectContaining({ order_by: 'frac_index', sort_direction: 'asc' })
      );
    }
  });

  it('does not log expected connectivity failures from delta polling', async () => {
    mocks.fetchCollectionItems.mockImplementation((_workspaceId, _collectionId, options) =>
      Promise.resolve(itemResult(options))
    );
    mocks.fetchCollectionBacklog.mockResolvedValue({
      items: [],
      pagination: { page: 1, page_size: 100, total_items: 0, total_pages: 0 },
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
      pagination: { page: 1, page_size: 100, total_items: 1, total_pages: 1 },
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

  it('keeps the current list page across a background refresh', async () => {
    mocks.fetchCollectionItems.mockClear();
    mocks.fetchCollectionItems.mockImplementation((_workspaceId, _collectionId, options) =>
      Promise.resolve(itemResult(options))
    );
    mocks.fetchCollectionBacklog.mockResolvedValue({
      items: [],
      pagination: { page: 1, page_size: 100, total_items: 0, total_pages: 0 },
      watermark: 1,
    });
    mocks.getBoardConfigurationBootstrap.mockResolvedValue({ board_configuration: null });

    mocks.routeSubscriber({ view: 'workspace-list', params: { id: '50' } });
    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));

    await collectionStore.setItemsPage(2, 50);
    expect(collectionStore.itemsPagination?.page).toBe(2);

    mocks.fetchCollectionItems.mockClear();
    await collectionStore.refresh();

    expect(mocks.fetchCollectionItems).toHaveBeenCalledTimes(1);
    expect(mocks.fetchCollectionItems).toHaveBeenCalledWith(
      '50',
      null,
      expect.objectContaining({ page: 2, limit: 50 })
    );
    expect(collectionStore.itemsPagination?.page).toBe(2);
  });

  it('polls from the oldest watermark returned by parallel board snapshots', async () => {
    mocks.fetchCollectionItems.mockImplementation((_workspaceId, _collectionId, options) =>
      Promise.resolve({ ...itemResult(options), watermark: 5 })
    );
    mocks.fetchCollectionBacklog.mockResolvedValue({
      items: [],
      pagination: { page: 1, page_size: 100, total_items: 0, total_pages: 0 },
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

describe('CollectionStore backlog refresh', () => {
  afterAll(() => collectionStore.destroy());
  afterEach(() => {
    vi.restoreAllMocks();
    mocks.fetchCollectionBacklog.mockReset();
  });

  // Mirrors the API: page_size is clamped to 100, so a single request cannot
  // return more than one page of an accumulated backlog.
  function backlogPage(options, { total = 250, titleFor150 = 'Item 150' } = {}) {
    const pageSize = Math.min(options.limit ?? 100, 100);
    const page = options.page ?? 1;
    const start = (page - 1) * pageSize;
    const items = [];
    for (let index = 0; index < pageSize; index++) {
      const id = start + index + 1;
      if (id > total) break;
      items.push({ id, status_id: 1, title: id === 150 ? titleFor150 : `Item ${id}` });
    }
    return {
      items,
      collectionName: 'Backlog',
      pagination: {
        page,
        page_size: pageSize,
        total_items: total,
        total_pages: Math.ceil(total / pageSize),
      },
      watermark: 10,
    };
  }

  it('preserves more than one page of loaded backlog across a background refresh', async () => {
    let titleFor150 = 'Item 150';
    mocks.fetchCollectionBacklog.mockImplementation((_ws, _col, options) =>
      Promise.resolve(backlogPage(options, { titleFor150 }))
    );

    mocks.routeSubscriber({ view: 'workspace-backlog', params: { id: '46' } });
    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));
    expect(collectionStore.backlogItems).toHaveLength(100);

    await collectionStore.loadMoreBacklog();
    expect(collectionStore.backlogItems).toHaveLength(200);

    titleFor150 = 'Updated after refresh';
    await collectionStore.refresh();

    expect(collectionStore.backlogItems).toHaveLength(200);
    expect(collectionStore.backlogItems.map((item) => item.id)).toEqual(
      Array.from({ length: 200 }, (_, index) => index + 1)
    );
    expect(collectionStore.backlogItems.find((item) => item.id === 150).title).toBe(
      'Updated after refresh'
    );
  });

  it('refreshes a backlog smaller than one page without extra requests', async () => {
    mocks.fetchCollectionBacklog.mockImplementation((_ws, _col, options) =>
      Promise.resolve(backlogPage(options, { total: 50 }))
    );

    mocks.routeSubscriber({ view: 'workspace-backlog', params: { id: '47' } });
    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));
    mocks.fetchCollectionBacklog.mockClear();

    await collectionStore.refresh();

    expect(mocks.fetchCollectionBacklog).toHaveBeenCalledTimes(1);
    expect(collectionStore.backlogItems).toHaveLength(50);
  });

  it('drops backlog rows the server no longer returns', async () => {
    let total = 250;
    mocks.fetchCollectionBacklog.mockImplementation((_ws, _col, options) =>
      Promise.resolve(backlogPage(options, { total }))
    );

    mocks.routeSubscriber({ view: 'workspace-backlog', params: { id: '48' } });
    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));
    await collectionStore.loadMoreBacklog();
    expect(collectionStore.backlogItems).toHaveLength(200);

    total = 150;
    await collectionStore.refresh();

    expect(collectionStore.backlogItems).toHaveLength(150);
    expect(collectionStore.backlogItems.some((item) => item.id > 150)).toBe(false);
  });
});

describe('CollectionStore list-view sorting', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });
  afterAll(() => collectionStore.destroy());

  it('sends server-side sort options from setSorting and resets to page 1', async () => {
    mocks.fetchCollectionItems.mockImplementation((_workspaceId, _collectionId, options) =>
      Promise.resolve({
        ...itemResult(options),
        sortableFields: ['key', 'status', 'priority'],
      })
    );
    mocks.fetchCollectionBacklog.mockResolvedValue({
      items: [],
      pagination: { page: 1, page_size: 100, total_items: 0, total_pages: 0 },
    });
    mocks.getBoardConfigurationBootstrap.mockResolvedValue({ board_configuration: null });

    mocks.routeSubscriber({ view: 'workspace-list', params: { id: '49' } });
    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));

    // The server's sortable fields surface in the store for the header UI.
    expect(collectionStore.sortableFields).toEqual(['key', 'status', 'priority']);

    mocks.fetchCollectionItems.mockClear();
    collectionStore.setSorting('priority', 'desc');

    // Sorting reloads from page 1 with the sort options on every page request.
    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));
    expect(mocks.fetchCollectionItems).toHaveBeenCalledTimes(1);
    expect(mocks.fetchCollectionItems).toHaveBeenCalledWith(
      '49',
      null,
      expect.objectContaining({
        page: 1,
        order_by: 'priority',
        sort_direction: 'desc',
      })
    );
  });
});

describe('CollectionStore local reconciliation coalescing', () => {
  beforeEach(async () => {
    mocks.fetchCollectionItems.mockImplementation((_workspaceId, _collectionId, options) =>
      Promise.resolve(itemResult(options))
    );
    mocks.fetchCollectionBacklog.mockResolvedValue({
      items: [],
      pagination: { page: 1, page_size: 100, total_items: 0, total_pages: 0 },
    });
    mocks.fetchCollectionItemChanges.mockResolvedValue({
      watermark: 10,
      changed_item_ids: [],
      removed_item_ids: [],
    });
    mocks.getBoardConfigurationBootstrap.mockResolvedValue({ board_configuration: null });

    mocks.routeSubscriber({ view: 'workspace-list', params: { id: '60' } });
    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));
    // Commit the cursor to 10 so later calls can assert the preserved value.
    await collectionStore.refreshDeltas();
    mocks.fetchCollectionItemChanges.mockClear();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('coalesces a burst of 20 local events into one reconciliation', async () => {
    vi.useFakeTimers();

    for (let i = 0; i < 20; i += 1) {
      collectionStore.scheduleLocalReconciliation();
    }
    await vi.advanceTimersByTimeAsync(200);

    expect(mocks.fetchCollectionItemChanges).toHaveBeenCalledTimes(1);
  });

  it('shares one delta fetch between concurrent direct reconciliation requests', async () => {
    let resolveFirst;
    mocks.fetchCollectionItemChanges.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveFirst = resolve;
        })
    );

    // SSE and cross-tab callbacks can fire in the same tick. Both must join
    // one physical delta fetch instead of issuing parallel reads.
    const first = collectionStore.refreshDeltas();
    const second = collectionStore.refreshDeltas();
    expect(mocks.fetchCollectionItemChanges).toHaveBeenCalledTimes(1);

    resolveFirst({ watermark: 11, changed_item_ids: [], removed_item_ids: [] });
    await expect(first).resolves.toBe(true);
    await expect(second).resolves.toBe(true);

    // The second caller still gets the trailing run it asked for.
    await vi.waitFor(() => expect(mocks.fetchCollectionItemChanges).toHaveBeenCalledTimes(2));
  });

  it('runs exactly one trailing reconciliation for events during in-flight work', async () => {
    vi.useFakeTimers();
    let resolveFirst;
    mocks.fetchCollectionItemChanges.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveFirst = resolve;
        })
    );

    collectionStore.scheduleLocalReconciliation();
    await vi.advanceTimersByTimeAsync(200);
    expect(mocks.fetchCollectionItemChanges).toHaveBeenCalledTimes(1);

    // Events that arrive while the first run is still in flight must not start
    // a parallel run; they set a pending flag for one trailing run.
    collectionStore.scheduleLocalReconciliation();
    await vi.advanceTimersByTimeAsync(200);
    expect(mocks.fetchCollectionItemChanges).toHaveBeenCalledTimes(1);

    resolveFirst({ watermark: 11, changed_item_ids: [], removed_item_ids: [] });
    await vi.advanceTimersByTimeAsync(0);

    expect(mocks.fetchCollectionItemChanges).toHaveBeenCalledTimes(2);
  });

  it('reports failure and preserves the cursor for replay', async () => {
    mocks.fetchCollectionItemChanges.mockRejectedValueOnce(new Error('offline'));

    await expect(collectionStore.refreshDeltas()).resolves.toBe(false);

    mocks.fetchCollectionItemChanges.mockResolvedValueOnce({
      watermark: 12,
      changed_item_ids: [],
      removed_item_ids: [],
    });
    await collectionStore.refreshDeltas();

    // The failed run must not have advanced the cursor past the committed 10.
    expect(mocks.fetchCollectionItemChanges).toHaveBeenLastCalledWith(
      '60',
      null,
      expect.objectContaining({ since: 10 })
    );
  });

  it('discards a reconciliation that resolves after a scope switch', async () => {
    let resolveChanges;
    mocks.fetchCollectionItemChanges.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveChanges = resolve;
        })
    );

    const pending = collectionStore.refreshDeltas();

    mocks.routeSubscriber({ view: 'workspace-list', params: { id: '61' } });
    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));

    resolveChanges({ watermark: 99, changed_item_ids: [1], removed_item_ids: [] });

    await expect(pending).resolves.toBe(false);
  });
});

describe('CollectionStore board configuration peek', () => {
  it('reports a pending then cached board configuration for the loaded scope', async () => {
    let resolveBootstrap;
    mocks.getBoardConfigurationBootstrap.mockReturnValue(
      new Promise((resolve) => {
        resolveBootstrap = resolve;
      })
    );

    const request = collectionStore.getBoardConfiguration('81', '71');
    expect(collectionStore.peekBoardConfiguration('81', '71')).toEqual({ pending: request });

    resolveBootstrap({
      board_configuration: { id: 3 },
      collection: null,
      referenced_workspace_ids: [],
      statuses: [],
    });
    await request;

    expect(collectionStore.peekBoardConfiguration('81', '71')).toEqual({ config: { id: 3 } });
  });

  it('returns null for a scope this store has not loaded', () => {
    expect(collectionStore.peekBoardConfiguration('999', '999')).toBeNull();
  });
});

describe('CollectionStore hierarchy continuation', () => {
  it('appends the next page for map and roadmap views and clears has-more', async () => {
    collectionStore.invalidate();
    mocks.fetchCollectionItems.mockImplementation((_workspaceId, _collectionId, options) =>
      Promise.resolve({
        items: Array.from({ length: options.page === 1 ? 250 : 50 }, (_, index) => ({
          id: (options.page - 1) * 250 + index + 1,
          status_id: 1,
        })),
        collectionName: 'Big hierarchy',
        // collectionService adds `limit` from the response page_size.
        pagination: { page: options.page, page_size: 250, limit: 250, total_items: 300, total_pages: 2 },
        watermark: 1,
      })
    );
    mocks.getBoardConfigurationBootstrap.mockResolvedValue({ board_configuration: null });

    mocks.routeSubscriber({ view: 'workspace-map', params: { id: '77' } });
    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));

    expect(collectionStore.items).toHaveLength(250);
    expect(collectionStore.itemsHasMore).toBe(true);
    expect(collectionStore.itemsRemainingCount).toBe(50);

    await collectionStore.loadMoreItems();

    expect(mocks.fetchCollectionItems).toHaveBeenLastCalledWith(
      '77',
      null,
      expect.objectContaining({ page: 2, limit: 250 })
    );
    expect(collectionStore.items).toHaveLength(300);
    expect(collectionStore.itemsHasMore).toBe(false);
    expect(collectionStore.itemsRemainingCount).toBe(0);
  });
});

describe('CollectionStore single-writer mutations', () => {
  beforeEach(() => {
    collectionStore.items = [];
    collectionStore.backlogItems = [];
    collectionStore.searchItems = [];
    collectionStore.boardConfiguration = null;
  });

  it('inserts created items into the right projection and deduplicates by id', () => {
    collectionStore.insertCreatedItem({ id: 1, status_id: 5 });
    collectionStore.insertCreatedItem({ id: 2, status_id: null }, { backlog: true });
    // A create event can arrive twice (modal plus delta or cross-tab frame).
    collectionStore.insertCreatedItem({ id: 1, status_id: 5 });
    collectionStore.insertCreatedItem({ id: 2, status_id: null }, { backlog: true });

    expect(collectionStore.items.map((item) => item.id)).toEqual([1]);
    expect(collectionStore.backlogItems.map((item) => item.id)).toEqual([2]);
  });

  it('patches the item across every projection that holds it', () => {
    const shared = { id: 7, status_id: 1, story_points: 1 };
    collectionStore.items = [{ ...shared }];
    collectionStore.backlogItems = [{ ...shared }];
    collectionStore.searchItems = [{ ...shared }];

    collectionStore.patchItem(7, { status_id: 2, story_points: 3 });

    expect(collectionStore.items[0]).toMatchObject({ status_id: 2, story_points: 3 });
    expect(collectionStore.backlogItems[0]).toMatchObject({ status_id: 2, story_points: 3 });
    expect(collectionStore.searchItems[0]).toMatchObject({ status_id: 2, story_points: 3 });
  });

  it('leaves projection identity untouched when the item is not loaded', () => {
    collectionStore.items = [{ id: 1 }];
    collectionStore.backlogItems = [{ id: 2 }];
    const items = collectionStore.items;
    const backlog = collectionStore.backlogItems;

    collectionStore.patchItem(99, { status_id: 3 });

    expect(collectionStore.items).toBe(items);
    expect(collectionStore.backlogItems).toBe(backlog);
  });

  it('moves a backlog row to the requested boundary', () => {
    collectionStore.backlogItems = [{ id: 1 }, { id: 2 }, { id: 3 }];

    collectionStore.moveBacklogItemToBoundary({ id: 3 }, 'start');
    expect(collectionStore.backlogItems.map((item) => item.id)).toEqual([3, 1, 2]);

    collectionStore.moveBacklogItemToBoundary({ id: 3 }, 'end');
    expect(collectionStore.backlogItems.map((item) => item.id)).toEqual([1, 2, 3]);
  });

  it('only caches a saved board configuration for the loaded scope', async () => {
    mocks.getBoardConfigurationBootstrap.mockResolvedValue({
      board_configuration: { id: 3 },
      collection: null,
      referenced_workspace_ids: [],
      statuses: [],
    });
    await collectionStore.getBoardConfiguration('81', '71');

    collectionStore.setBoardConfiguration({ id: 4 }, '81', '71');
    expect(collectionStore.boardConfiguration).toEqual({ id: 4 });

    collectionStore.setBoardConfiguration({ id: 5 }, '82', '72');
    expect(collectionStore.boardConfiguration).toEqual({ id: 4 });
  });
});

describe('CollectionStore deferred collection count', () => {
  beforeEach(() => {
    mocks.fetchCollectionItems.mockImplementation((_workspaceId, _collectionId, options) =>
      Promise.resolve(itemResult(options))
    );
    mocks.fetchCollectionBacklog.mockResolvedValue({
      items: [],
      pagination: { page: 1, page_size: 100, total_items: 0, total_pages: 0 },
    });
    mocks.fetchCollectionItemChanges.mockResolvedValue({
      watermark: 100,
      changed_item_ids: [],
      removed_item_ids: [],
    });
    mocks.getBoardConfigurationBootstrap.mockResolvedValue({ board_configuration: null });
    mocks.fetchCollectionTotal.mockReset();
    mocks.fetchCollectionTotal.mockResolvedValue({ total: 300, watermark: 100 });
  });

  afterEach(() => {
    vi.restoreAllMocks();
    collectionStore.collectionTotal = null;
  });

  it('publishes item rows before the count resolves', async () => {
    let resolveCount;
    mocks.fetchCollectionTotal.mockReturnValue(
      new Promise((resolve) => {
        resolveCount = resolve;
      })
    );

    mocks.routeSubscriber({ view: 'workspace-list', params: { id: '90' } });
    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));

    // Rows are visible while the count is still in flight.
    expect(collectionStore.items).toHaveLength(1);
    expect(collectionStore.collectionTotal).toBeNull();
    expect(mocks.fetchCollectionTotal).toHaveBeenCalledTimes(1);

    resolveCount({ total: 7, watermark: 100 });
    await vi.waitFor(() => expect(collectionStore.collectionTotal).toBe(7));
  });

  it('folds many invalidations during a pending count into one follow-up', async () => {
    let resolveCount;
    mocks.fetchCollectionTotal.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveCount = resolve;
      })
    );
    mocks.fetchCollectionTotal.mockResolvedValue({ total: 8, watermark: 100 });

    mocks.routeSubscriber({ view: 'workspace-list', params: { id: '91' } });
    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));
    expect(mocks.fetchCollectionTotal).toHaveBeenCalledTimes(1);

    for (let i = 0; i < 20; i += 1) {
      collectionStore.applyItem({ id: 1, status_id: 1 });
    }
    // All 20 collapse into the dirty flag; no parallel count reads.
    expect(mocks.fetchCollectionTotal).toHaveBeenCalledTimes(1);

    resolveCount({ total: 7, watermark: 100 });
    await vi.waitFor(() => expect(mocks.fetchCollectionTotal).toHaveBeenCalledTimes(2));
    await vi.waitFor(() => expect(collectionStore.collectionTotal).toBe(8));
  });

  it('keeps the last known count when a refresh fails', async () => {
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    mocks.fetchCollectionTotal.mockResolvedValueOnce({ total: 12, watermark: 100 });

    mocks.routeSubscriber({ view: 'workspace-list', params: { id: '92' } });
    await vi.waitFor(() => expect(collectionStore.collectionTotal).toBe(12));

    mocks.fetchCollectionTotal.mockRejectedValueOnce(new Error('offline'));
    collectionStore.applyItem({ id: 1, status_id: 1 });
    await vi.waitFor(() => expect(mocks.fetchCollectionTotal).toHaveBeenCalledTimes(2));
    await vi.waitFor(() => expect(errorSpy).toHaveBeenCalled());

    // A failed refresh keeps the stale value; it never degrades to 0 or null.
    expect(collectionStore.collectionTotal).toBe(12);
  });

  it('discards a count that resolves after a scope switch', async () => {
    let resolveFirst;
    mocks.fetchCollectionTotal.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFirst = resolve;
      })
    );
    mocks.fetchCollectionTotal.mockResolvedValue({ total: 50, watermark: 100 });

    mocks.routeSubscriber({ view: 'workspace-list', params: { id: '93' } });
    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));

    mocks.routeSubscriber({ view: 'workspace-list', params: { id: '94' } });
    await vi.waitFor(() => expect(collectionStore.loading).toBe(false));
    await vi.waitFor(() => expect(collectionStore.collectionTotal).toBe(50));

    resolveFirst({ total: 999, watermark: 100 });
    await Promise.resolve();
    await Promise.resolve();

    expect(collectionStore.collectionTotal).toBe(50);
  });

  it('requests the unfiltered count for the loaded scope', async () => {
    mocks.routeSubscriber({ view: 'workspace-list', params: { id: '95' } });
    await vi.waitFor(() => expect(collectionStore.collectionTotal).toBe(300));

    expect(mocks.fetchCollectionTotal).toHaveBeenCalledWith(
      '95',
      null,
      expect.objectContaining({ readPolicy: { priority: 'background' } })
    );
  });

  it('combines a late count watermark as a minimum for the delta cursor', async () => {
    mocks.fetchCollectionItems.mockImplementation((_workspaceId, _collectionId, options) =>
      Promise.resolve({ ...itemResult(options), watermark: 10 })
    );
    mocks.fetchCollectionTotal.mockResolvedValue({ total: 3, watermark: 4 });

    mocks.routeSubscriber({ view: 'workspace-list', params: { id: '96' } });
    await vi.waitFor(() => expect(collectionStore.collectionTotal).toBe(3));

    await collectionStore.refreshDeltas();

    expect(mocks.fetchCollectionItemChanges).toHaveBeenLastCalledWith(
      '96',
      null,
      expect.objectContaining({ since: 4 })
    );
  });

  it('does not refetch the count for a delta with no loaded changes', async () => {
    mocks.routeSubscriber({ view: 'workspace-list', params: { id: '97' } });
    await vi.waitFor(() => expect(collectionStore.collectionTotal).toBe(300));
    const countCalls = mocks.fetchCollectionTotal.mock.calls.length;

    await collectionStore.refreshDeltas();

    expect(mocks.fetchCollectionTotal).toHaveBeenCalledTimes(countCalls);
  });
});
