import { beforeEach, describe, expect, test, vi } from 'vitest';
import { items } from '../api/items.js';
import { createWorkItemSearchStore } from './searchStore.svelte.js';

vi.mock('../api/items.js', () => ({
  items: { getAll: vi.fn() },
}));

beforeEach(() => vi.resetAllMocks());

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

describe('createWorkItemSearchStore request ordering', () => {
  test('a superseded success cannot overwrite newer results', async () => {
    const store = createWorkItemSearchStore({ allowEmptyQuery: true });
    let state;
    store.subscribe((value) => {
      state = value;
    });
    const first = deferred();
    const second = deferred();
    items.getAll
      .mockImplementationOnce(() => first.promise)
      .mockImplementationOnce(() => second.promise);

    const firstRun = store.executeSearch({ page: 1, limit: 50 });
    const secondRun = store.executeSearch({ page: 2, limit: 50 });

    second.resolve({
      data: [{ id: 2 }],
      pagination: { page: 2, page_size: 50, total_items: 1, total_pages: 1 },
    });
    await secondRun;
    expect(state.workItems).toEqual([{ id: 2 }]);

    first.resolve({
      data: [{ id: 1 }],
      pagination: { page: 1, page_size: 50, total_items: 1, total_pages: 1 },
    });
    await firstRun;
    expect(state.workItems).toEqual([{ id: 2 }]);
    expect(state.pagination.page).toBe(2);
  });

  test('a stale failure leaves newer results and error state intact', async () => {
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    const store = createWorkItemSearchStore({ allowEmptyQuery: true });
    let state;
    store.subscribe((value) => {
      state = value;
    });
    const first = deferred();
    const second = deferred();
    items.getAll
      .mockImplementationOnce(() => first.promise)
      .mockImplementationOnce(() => second.promise);

    const firstRun = store.executeSearch();
    const secondRun = store.executeSearch();

    second.resolve({ data: [{ id: 2 }], pagination: null });
    await secondRun;
    first.reject(new Error('stale failure'));
    await firstRun;
    errorSpy.mockRestore();

    expect(state.workItems).toEqual([{ id: 2 }]);
    expect(state.qlError).toBe(null);
  });

  test('the current failure clears results and reports the error', async () => {
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    const store = createWorkItemSearchStore({ allowEmptyQuery: true });
    let state;
    store.subscribe((value) => {
      state = value;
    });
    const failing = deferred();
    items.getAll.mockImplementationOnce(() => failing.promise);

    const run = store.executeSearch();
    failing.reject(new Error('boom'));
    await run;
    errorSpy.mockRestore();

    expect(state.qlError).toBe('boom');
    expect(state.workItems).toEqual([]);
    expect(state.loadingItems).toBe(false);
  });
});

test('collection searches treat an empty query as unrestricted and preserve v2 pagination', async () => {
  const store = createWorkItemSearchStore({ allowEmptyQuery: true });
  let state;
  const unsubscribe = store.subscribe((value) => {
    state = value;
  });
  const pagination = { page: 2, page_size: 25, total_items: 26, total_pages: 2 };
  items.getAll.mockResolvedValue({ data: [{ id: 26 }], pagination });
  try {
    await store.executeSearch({ page: 2, limit: 25 });
    expect(items.getAll).toHaveBeenCalledWith({ ql: '', page: 2, limit: 25 });
    expect(state.workItems).toEqual([{ id: 26 }]);
    expect(state.pagination).toEqual(pagination);
  } finally {
    unsubscribe();
  }
});

test('an unconfigured global search waits for a query', async () => {
  const store = createWorkItemSearchStore();
  await store.executeSearch();
  expect(items.getAll).not.toHaveBeenCalled();
});

describe('createWorkItemSearchStore URL round-trip', () => {
  const nullFilter = {
    field: { id: 'cf_due', type: 'date', name: 'Due' },
    operator: 'IS NULL',
    value: '',
    values: [],
  };

  test('serializes and restores a value-free null filter', () => {
    window.history.replaceState({}, '', '/');
    const store = createWorkItemSearchStore();
    store.setDynamicFilters([nullFilter]);
    store.syncToURL();

    const params = new URLSearchParams(window.location.search);
    expect(JSON.parse(params.get('dynamicFilters'))).toEqual([nullFilter]);

    const restored = createWorkItemSearchStore();
    let state;
    restored.subscribe((value) => {
      state = value;
    });
    restored.restoreFromURL();
    expect(state.dynamicFilters).toEqual([nullFilter]);
  });

  test('keeps null filters alongside value filters', () => {
    window.history.replaceState({}, '', '/');
    const store = createWorkItemSearchStore();
    const valueFilter = {
      field: { id: 'cf_stage', type: 'text' },
      operator: '=',
      value: 'draft',
      values: [],
    };
    store.setSelectedStatuses([3]);
    store.setDynamicFilters([
      { ...nullFilter, operator: 'IS NOT NULL' },
      valueFilter,
    ]);
    store.syncToURL();

    const params = new URLSearchParams(window.location.search);
    expect(params.get('statuses')).toBe('3');
    expect(JSON.parse(params.get('dynamicFilters'))).toEqual([
      { ...nullFilter, operator: 'IS NOT NULL' },
      valueFilter,
    ]);
  });

  test('a null-only filter counts as an active filter', () => {
    const store = createWorkItemSearchStore();
    let state;
    store.subscribe((value) => {
      state = value;
    });
    store.setDynamicFilters([nullFilter]);
    expect(state.hasFilters).toBe(true);
  });
});

describe('createWorkItemSearchStore workspace queries', () => {
  test('serializes selected workspace IDs as stable workspace keys', () => {
    const store = createWorkItemSearchStore();
    let state;
    const unsubscribe = store.subscribe((value) => {
      state = value;
    });

    store.setWorkspaces([
      { id: 7, key: 'WI', name: 'Windshift' },
      { id: 8, key: 'OPS', name: 'Operations' },
    ]);
    store.setSelectedWorkspaces([7, 8]);

    expect(state.qlQuery).toBe('workspaceKey IN ("WI", "OPS")');
    unsubscribe();
  });
});
