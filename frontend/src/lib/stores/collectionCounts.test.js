import { afterAll, afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { items } from '../api/items.js';
import { collections } from '../api/collections.js';


const mocks = vi.hoisted(() => ({ list: vi.fn(), backlog: vi.fn(), changes: vi.fn() }));
vi.mock('../api/items.js', () => ({
  items: { getAll: mocks.list, getBacklog: mocks.backlog, getChanges: mocks.changes },
}))

vi.mock('../api/collections.js', () => ({
  collections: {
        get: async () => ({ name: 'Collection' }),
        getBoardConfigurationBootstrap: async () => null,
      },
  collectionCategories: { getAll: vi.fn().mockResolvedValue([]) },
}));
vi.mock('../router.js', () => ({
  currentRoute: { subscribe: () => () => {} },
  GLOBAL_COLLECTION_VIEWS: new Set(),
}));
vi.mock('./workspaceDataStore.svelte.js', () => ({
  workspaceDataStore: {
    statuses: [],
    initializeGlobal: async () => {},
    initialize: async () => {},
  },
}));
const { collectionStore } = await import('./collectionContext.svelte.js');
const { workspaceDataStore } = await import('./workspaceDataStore.svelte.js');
const response = (total, data = [{ id: 1 }]) => ({
  data,
  pagination: { page: 1, page_size: 100, total_items: total, total_pages: 1 },
  meta: { watermark: 1 },
});

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  mocks.changes.mockReset();
  mocks.list.mockImplementation(async (filters) => response(filters.limit === 1 ? 300 : 80));
  mocks.backlog.mockResolvedValue(response(20));
});
afterEach(() => {
  workspaceDataStore.statuses = [];
  vi.restoreAllMocks();
});
afterAll(() => collectionStore.destroy());

describe('collection membership counts', () => {
  it.each(['board', 'list', 'tree', 'map', 'roadmap', 'backlog'])(
    'keeps the collection total separate from %s results',
    async (view) => {
      await collectionStore.load(null, `collection-${view}`, `collection-${view}`);
      // The count is deferred; rows paint first and the total lands after.
      await vi.waitFor(() => expect(collectionStore.collectionTotal).toBe(300));
      expect(mocks.list).toHaveBeenCalledWith(
        {
          collection_id: `collection-${view}`,
          page: 1,
          limit: 1,
          omit_descriptions: true,
          include_watermark: true,
        },
        expect.objectContaining({ readPolicy: { priority: 'background' } })
      );
      if (view === 'backlog') expect(collectionStore.backlogPagination.total_items).toBe(20);
      else expect(collectionStore.itemsPagination.total_items).toBe(80);
    }
  );

  it('requests a workspace total without view filters and preserves zero', async () => {
    mocks.list.mockResolvedValue(response(0, []));
    await collectionStore.load('empty-workspace', null, 'workspace-list');
    await vi.waitFor(() => expect(collectionStore.collectionTotal).toBe(0));
    expect(mocks.list).toHaveBeenCalledWith(
      {
        workspace_id: 'empty-workspace',
        page: 1,
        limit: 1,
        omit_descriptions: true,
        include_watermark: true,
      },
      expect.objectContaining({ readPolicy: { priority: 'background' } })
    );
  });

  it('does not narrow the collection total when a sub-filter changes', async () => {
    await collectionStore.load(null, 'filtered', 'collection-list');
    await vi.waitFor(() => expect(collectionStore.collectionTotal).toBe(300));

    collectionStore.subFilterQL = 'priority = "High"';
    await collectionStore.load(null, 'filtered', 'collection-list');
    await vi.waitFor(() => expect(collectionStore.collectionTotal).toBe(300));

    // Every count read stays unfiltered: the exact filters carry no sub_ql.
    expect(mocks.list).toHaveBeenCalledWith(
      {
        collection_id: 'filtered',
        page: 1,
        limit: 1,
        omit_descriptions: true,
        include_watermark: true,
      },
      expect.objectContaining({ readPolicy: { priority: 'background' } })
    );
  });

  it('polls from the older count snapshot when items load after a concurrent mutation', async () => {
    mocks.list.mockImplementation(async (filters) => ({
      ...response(filters.limit === 1 ? 300 : 80),
      meta: { watermark: filters.limit === 1 ? 1 : 3 },
    }));
    mocks.changes.mockResolvedValue({ watermark: 3, changed_item_ids: [], removed_item_ids: [] });

    await collectionStore.load(null, 'concurrent', 'collection-list');
    // The late count snapshot lowers the cursor to the older watermark.
    await vi.waitFor(() => expect(collectionStore.collectionTotal).toBe(300));
    await collectionStore.refreshDeltas();

    expect(mocks.changes).toHaveBeenCalledWith(
      {
        collection_id: 'concurrent',
        since: 1,
        sub_ql: 'status_completed = false',
      },
      expect.any(Object)
    );
  });

  it('refreshes totals after deletion of an unloaded item', async () => {
    await collectionStore.load(null, 'delete', 'collection-list');
    await vi.waitFor(() => expect(collectionStore.collectionTotal).toBe(300));

    mocks.list.mockImplementation(async (filters) => response(filters.limit === 1 ? 299 : 79));
    mocks.changes.mockResolvedValue({
      watermark: 2,
      removed_item_ids: [250],
      changed_item_ids: [],
    });
    await collectionStore.refreshDeltas();

    await vi.waitFor(() => expect(collectionStore.collectionTotal).toBe(299));
    expect(collectionStore.itemsPagination.total_items).toBe(79);
  });

  it('retains the total when a delta removes an unrelated item', async () => {
    await collectionStore.load(null, 'unrelated', 'collection-list');
    await vi.waitFor(() => expect(collectionStore.collectionTotal).toBe(300));

    mocks.changes.mockResolvedValue({
      watermark: 2,
      removed_item_ids: [999],
      changed_item_ids: [],
    });
    await collectionStore.refreshDeltas();

    await vi.waitFor(() => expect(collectionStore.collectionTotal).toBe(300));
    expect(collectionStore.itemsPagination.total_items).toBe(80);
  });

  it('keeps the unfiltered total when a completed item is hidden', async () => {
    workspaceDataStore.statuses = [{ id: 9, is_completed: true }];
    await collectionStore.load(null, 'hidden', 'collection-list');
    await vi.waitFor(() => expect(collectionStore.collectionTotal).toBe(300));

    collectionStore.applyItem({ id: 1, status_id: 9 });

    // Hiding a completed row trims the loaded page, not the unfiltered total.
    expect(collectionStore.collectionTotal).toBe(300);
    expect(collectionStore.itemsPagination.total_items).toBe(79);
  });

  it('does not substitute zero or view matches when the count read fails', async () => {
    const log = vi.spyOn(console, 'error').mockImplementation(() => {});
    mocks.list.mockImplementation(async (filters) => {
      if (filters.limit === 1) throw new Error('Count unavailable');
      return response(80);
    });
    await collectionStore.load(null, 'failed', 'collection-list');

    await vi.waitFor(() =>
      expect(log).toHaveBeenCalledWith(
        '[collectionStore] Count refresh failed:',
        expect.objectContaining({ message: 'Count unavailable' })
      )
    );
    // A failed count on a fresh scope stays unknown; it is never shown as 0.
    expect(collectionStore.collectionTotal).toBeNull();
    expect(collectionStore.itemsPagination.total_items).toBe(80);
  });

  it('discards an old collection count that resolves after navigation', async () => {
    let resolveOld;
    mocks.list.mockImplementation((filters) => {
      if (filters.collection_id === 'old' && filters.limit === 1) {
        return new Promise((resolve) => {
          resolveOld = resolve;
        });
      }
      return Promise.resolve(response(filters.limit === 1 ? 42 : 10));
    });
    const oldLoad = collectionStore.load(null, 'old', 'collection-list');
    await vi.waitFor(() => expect(resolveOld).toBeTypeOf('function'));

    await collectionStore.load(null, 'new', 'collection-backlog');
    await vi.waitFor(() => expect(collectionStore.collectionTotal).toBe(42));

    resolveOld(response(999));
    await oldLoad;
    await Promise.resolve();
    await Promise.resolve();
    expect(collectionStore.collectionTotal).toBe(42);
  });
});
