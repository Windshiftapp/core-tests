import { get } from 'svelte/store';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

// Mock the api module before importing the store — the store calls
// workspaces.* at runtime.
vi.mock('../api/workspaces.js', () => ({
  workspaces: {
        get: vi.fn(),
        getAll: vi.fn(),
        getPage: vi.fn(),
        getOrCreatePersonal: vi.fn(),
      },
}));

import { workspaces } from '../api/workspaces.js';
import { currentWorkspace, workspacesStore } from './workspaces.svelte.js';

function deferred() {
  let resolve;
  const promise = new Promise((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
}

// getPage resolves with a paged document; this helper wraps a row list.
function pageDocument(rows, total = rows.length) {
  return { data: rows, pagination: { page: 1, page_size: 200, total, total_pages: 1 } };
}

beforeEach(() => {
  currentWorkspace.clear();
  workspacesStore.clear();
  // resetAllMocks drains the mockResolvedValueOnce / mockRejectedValueOnce
  // queues — clearAllMocks doesn't, which would leak rigged return values
  // from one test into the next.
  vi.resetAllMocks();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('currentWorkspace.load', () => {
  test('hydrates an already-fetched workspace without an API request', () => {
    const workspace = { id: 7, name: 'Shared snapshot' };

    currentWorkspace.hydrate(workspace);

    expect(workspaces.get).not.toHaveBeenCalled();
    expect(get(currentWorkspace)).toEqual(workspace);
  });

  test('fetches the workspace and stores it', async () => {
    const ws = { id: 1, name: 'Alpha' };
    workspaces.get.mockResolvedValueOnce(ws);

    await currentWorkspace.load(1);

    expect(workspaces.get).toHaveBeenCalledWith(1);
    expect(get(currentWorkspace)).toEqual(ws);
  });

  test('clears state when called without an id', async () => {
    workspaces.get.mockResolvedValueOnce({ id: 1 });
    await currentWorkspace.load(1);

    await currentWorkspace.load(null);

    expect(get(currentWorkspace)).toBeNull();
  });

  test('deduplicates back-to-back calls with the same id', async () => {
    workspaces.get.mockResolvedValueOnce({ id: 1 });
    await currentWorkspace.load(1);
    await currentWorkspace.load(1);
    expect(workspaces.get).toHaveBeenCalledTimes(1);
  });

  test('reloads when the id changes', async () => {
    workspaces.get
      .mockResolvedValueOnce({ id: 1, name: 'A' })
      .mockResolvedValueOnce({ id: 2, name: 'B' });
    await currentWorkspace.load(1);
    await currentWorkspace.load(2);
    expect(workspaces.get).toHaveBeenCalledTimes(2);
    expect(get(currentWorkspace)).toEqual({ id: 2, name: 'B' });
  });

  test('does not let a slower previous workspace replace the current route', async () => {
    const first = deferred();
    const second = deferred();
    workspaces.get.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);

    const firstLoad = currentWorkspace.load(1);
    const secondLoad = currentWorkspace.load(2);

    second.resolve({ id: 2, name: 'B' });
    await secondLoad;
    first.resolve({ id: 1, name: 'A' });
    await firstLoad;

    expect(get(currentWorkspace)).toEqual({ id: 2, name: 'B' });
  });

  test('on API failure: clears state and logs', async () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    workspaces.get.mockRejectedValueOnce(new Error('500'));
    await currentWorkspace.load(7);
    expect(get(currentWorkspace)).toBeNull();
    expect(errSpy).toHaveBeenCalled();
  });

  test('after a failed load, retrying with the same id re-fetches (no stale dedupe)', async () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    workspaces.get.mockRejectedValueOnce(new Error('500'));
    await currentWorkspace.load(7);
    expect(errSpy).toHaveBeenCalled();

    // lastWorkspaceId is only set on a successful fetch, so the second call
    // with the same id retries instead of being deduped against the failed one.
    workspaces.get.mockResolvedValueOnce({ id: 7 });
    await currentWorkspace.load(7);
    expect(workspaces.get).toHaveBeenCalledTimes(2);
    expect(get(currentWorkspace)).toEqual({ id: 7 });
  });

  test('can return to the previous workspace after a different workspace fails to load', async () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    workspaces.get
      .mockResolvedValueOnce({ id: 1, name: 'A' })
      .mockRejectedValueOnce(new Error('workspace B unavailable'))
      .mockResolvedValueOnce({ id: 1, name: 'A reloaded' });

    await currentWorkspace.load(1);
    await currentWorkspace.load(2);
    expect(errSpy).toHaveBeenCalled();
    expect(get(currentWorkspace)).toBeNull();

    await currentWorkspace.load(1);

    expect(workspaces.get).toHaveBeenCalledTimes(3);
    expect(get(currentWorkspace)).toEqual({ id: 1, name: 'A reloaded' });
  });
});

describe('currentWorkspace.patch', () => {
  test('merges partial updates onto the current workspace', async () => {
    workspaces.get.mockResolvedValueOnce({ id: 1, name: 'A', key: 'AAA' });
    await currentWorkspace.load(1);
    currentWorkspace.patch({ name: 'A renamed' });
    expect(get(currentWorkspace)).toEqual({ id: 1, name: 'A renamed', key: 'AAA' });
  });

  test('is a no-op when there is no current workspace', () => {
    currentWorkspace.patch({ name: 'x' });
    expect(get(currentWorkspace)).toBeNull();
  });
});

describe('currentWorkspace.clear', () => {
  test('resets the workspace AND the dedupe cache so a subsequent load fetches', async () => {
    workspaces.get
      .mockResolvedValueOnce({ id: 1, name: 'A' })
      .mockResolvedValueOnce({ id: 1, name: 'A reloaded' });

    await currentWorkspace.load(1);
    currentWorkspace.clear();
    await currentWorkspace.load(1);

    expect(workspaces.get).toHaveBeenCalledTimes(2);
    expect(get(currentWorkspace)).toEqual({ id: 1, name: 'A reloaded' });
  });

  test('does not restore a workspace from a request that resolves after clear', async () => {
    const pending = deferred();
    workspaces.get.mockReturnValueOnce(pending.promise);

    const load = currentWorkspace.load(1);
    currentWorkspace.clear();
    pending.resolve({ id: 1, name: 'Old account workspace' });
    await load;

    expect(get(currentWorkspace)).toBeNull();
  });
});

describe('workspacesStore.load', () => {
  test('fetches only the first directory page and records the total', async () => {
    workspaces.getPage.mockResolvedValueOnce(
      pageDocument([{ id: 1, name: 'A' }, { id: 2, name: 'B', is_personal: true }], 7500)
    );

    await workspacesStore.load();

    expect(workspaces.getPage).toHaveBeenCalledWith({ page: 1, page_size: 200 });
    expect(workspaces.getAll).not.toHaveBeenCalled();
    const state = get(workspacesStore);
    expect(state.workspaces).toHaveLength(2);
    expect(state.total).toBe(7500);
    expect(state.truncated).toBe(true);
    expect(state.loaded).toBe(true);
    expect(state.loading).toBe(false);
  });

  test('is not truncated when the cache covers the whole directory', async () => {
    workspaces.getPage.mockResolvedValueOnce(pageDocument([{ id: 1, name: 'A' }]));

    await workspacesStore.load();

    expect(get(workspacesStore).truncated).toBe(false);
  });

  test('falls back to empty array when API returns null', async () => {
    workspaces.getPage.mockResolvedValueOnce(null);
    await workspacesStore.load();
    expect(get(workspacesStore).workspaces).toEqual([]);
    expect(get(workspacesStore).loaded).toBe(true);
  });

  test('on API failure: empty list, total reset, loaded=true, error logged', async () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    workspaces.getPage.mockRejectedValueOnce(new Error('500'));
    await workspacesStore.load();
    expect(get(workspacesStore).workspaces).toEqual([]);
    expect(get(workspacesStore).total).toBe(0);
    expect(get(workspacesStore).loaded).toBe(true);
    expect(get(workspacesStore).loading).toBe(false);
    expect(errSpy).toHaveBeenCalled();
  });

  test('shares an in-flight list request and reuses the loaded catalog', async () => {
    const pending = deferred();
    workspaces.getPage.mockReturnValueOnce(pending.promise);

    const first = workspacesStore.load();
    const second = workspacesStore.load();
    expect(workspaces.getPage).toHaveBeenCalledOnce();

    const all = [{ id: 1, name: 'Shared' }];
    pending.resolve(pageDocument(all));
    await expect(Promise.all([first, second])).resolves.toEqual([all, all]);
    await expect(workspacesStore.load()).resolves.toEqual(all);
    expect(workspaces.getPage).toHaveBeenCalledOnce();
  });
});

describe('workspacesStore.searchWorkspaces', () => {
  test('searches server-side and returns matches with the match total', async () => {
    workspaces.getPage.mockResolvedValueOnce(
      pageDocument([{ id: 5, name: 'Platform' }], 3)
    );

    const result = await workspacesStore.searchWorkspaces('plat');

    expect(workspaces.getPage).toHaveBeenCalledWith({ page: 1, page_size: 25, search: 'plat' });
    expect(result).toEqual({ workspaces: [{ id: 5, name: 'Platform' }], total: 3 });
  });

  test('trims the query and forwards an explicit limit', async () => {
    workspaces.getPage.mockResolvedValueOnce(pageDocument([]));

    await workspacesStore.searchWorkspaces('  web  ', { limit: 10 });

    expect(workspaces.getPage).toHaveBeenCalledWith({ page: 1, page_size: 10, search: 'web' });
  });

  test('shares one request for identical concurrent queries', async () => {
    const pending = deferred();
    workspaces.getPage.mockReturnValueOnce(pending.promise);

    const first = workspacesStore.searchWorkspaces('web');
    const second = workspacesStore.searchWorkspaces('web');
    expect(workspaces.getPage).toHaveBeenCalledOnce();

    pending.resolve(pageDocument([{ id: 1 }], 1));
    await expect(first).resolves.toEqual({ workspaces: [{ id: 1 }], total: 1 });
    await expect(second).resolves.toEqual({ workspaces: [{ id: 1 }], total: 1 });
  });

  test('a newer query supersedes an in-flight older one', async () => {
    const first = deferred();
    const second = deferred();
    workspaces.getPage
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);

    const firstSearch = workspacesStore.searchWorkspaces('old');
    const secondSearch = workspacesStore.searchWorkspaces('new');

    second.resolve(pageDocument([{ id: 2, name: 'New match' }], 1));
    await expect(secondSearch).resolves.toEqual({
      workspaces: [{ id: 2, name: 'New match' }],
      total: 1,
    });

    first.resolve(pageDocument([{ id: 1, name: 'Stale match' }], 1));
    // The stale response must not be delivered to its caller.
    await expect(firstSearch).resolves.toEqual({ workspaces: [], total: 0 });
  });

  test('does not leak a superseded request as the shared in-flight promise', async () => {
    const pending = deferred();
    workspaces.getPage.mockReturnValueOnce(pending.promise);
    const firstSearch = workspacesStore.searchWorkspaces('old');

    workspaces.getPage.mockResolvedValueOnce(pageDocument([{ id: 9 }], 4));
    const secondSearch = workspacesStore.searchWorkspaces('new');
    await secondSearch;

    pending.resolve(pageDocument([], 0));
    await firstSearch;

    // After both settle, a fresh query issues a new request.
    workspaces.getPage.mockResolvedValueOnce(pageDocument([{ id: 3 }], 1));
    await workspacesStore.searchWorkspaces('fresh');
    expect(workspaces.getPage).toHaveBeenCalledTimes(3);
  });

  test('returns empty results and logs on API failure', async () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    workspaces.getPage.mockRejectedValueOnce(new Error('500'));

    const result = await workspacesStore.searchWorkspaces('web');

    expect(result).toEqual({ workspaces: [], total: 0 });
    expect(errSpy).toHaveBeenCalled();
  });
});

describe('workspacesStore — regularWorkspaces derived', () => {
  test('filters out personal workspaces', async () => {
    workspaces.getPage.mockResolvedValueOnce(
      pageDocument([
        { id: 1, name: 'A', is_personal: false },
        { id: 2, name: 'B', is_personal: true },
        { id: 3, name: 'C' }, // no flag = regular
      ])
    );
    await workspacesStore.load();

    const ids = get(workspacesStore).regularWorkspaces.map((w) => w.id);
    expect(ids).toEqual([1, 3]);
  });
});

describe('workspacesStore.loadPersonalWorkspace', () => {
  test('stores and returns the personal workspace', async () => {
    const personal = { id: 99, is_personal: true };
    workspaces.getOrCreatePersonal.mockResolvedValueOnce(personal);

    const result = await workspacesStore.loadPersonalWorkspace();

    expect(result).toEqual(personal);
    expect(get(workspacesStore).personalWorkspace).toEqual(personal);
  });

  test('returns null and logs on API failure', async () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    workspaces.getOrCreatePersonal.mockRejectedValueOnce(new Error('boom'));
    const result = await workspacesStore.loadPersonalWorkspace();
    expect(result).toBeNull();
    expect(get(workspacesStore).personalWorkspace).toBeNull();
    expect(errSpy).toHaveBeenCalled();
  });

  test('shares an in-flight personal workspace request and reuses its result', async () => {
    const pending = deferred();
    workspaces.getOrCreatePersonal.mockReturnValueOnce(pending.promise);

    const first = workspacesStore.loadPersonalWorkspace();
    const second = workspacesStore.loadPersonalWorkspace();
    expect(workspaces.getOrCreatePersonal).toHaveBeenCalledOnce();

    const personal = { id: 99, is_personal: true };
    pending.resolve(personal);
    await expect(Promise.all([first, second])).resolves.toEqual([personal, personal]);
    await expect(workspacesStore.loadPersonalWorkspace()).resolves.toBe(personal);
    expect(workspaces.getOrCreatePersonal).toHaveBeenCalledOnce();
  });
});

describe('workspacesStore CRUD-style mutations', () => {
  test('add appends a workspace and grows the directory total', async () => {
    workspaces.getPage.mockResolvedValueOnce(pageDocument([{ id: 1, name: 'A' }], 1));
    await workspacesStore.load();

    workspacesStore.add({ id: 2, name: 'B' });

    expect(get(workspacesStore).workspaces.map((w) => w.id)).toEqual([1, 2]);
    expect(get(workspacesStore).total).toBe(2);
  });

  test('add is not overwritten by a workspace list request started before creation', async () => {
    const pending = deferred();
    workspaces.getPage.mockReturnValueOnce(pending.promise);

    const load = workspacesStore.load();
    workspacesStore.add({ id: 2, name: 'Created workspace' });
    pending.resolve(pageDocument([{ id: 1, name: 'Existing workspace' }]));
    await load;

    expect(get(workspacesStore).workspaces).toEqual([{ id: 2, name: 'Created workspace' }]);
  });

  test('updateWorkspace merges updates onto a single id', () => {
    workspacesStore.add({ id: 1, name: 'A' });
    workspacesStore.add({ id: 2, name: 'B' });

    workspacesStore.updateWorkspace(2, { name: 'B renamed', key: 'XYZ' });

    expect(get(workspacesStore).workspaces).toEqual([
      { id: 1, name: 'A' },
      { id: 2, name: 'B renamed', key: 'XYZ' },
    ]);
  });

  test('updateWorkspace is a no-op when the id is not present', () => {
    workspacesStore.add({ id: 1, name: 'A' });
    workspacesStore.updateWorkspace(99, { name: 'oops' });
    expect(get(workspacesStore).workspaces).toEqual([{ id: 1, name: 'A' }]);
  });

  test('remove drops a workspace by id and shrinks the directory total', async () => {
    workspaces.getPage.mockResolvedValueOnce(
      pageDocument([{ id: 1 }, { id: 2 }], 2)
    );
    await workspacesStore.load();

    workspacesStore.remove(1);

    expect(get(workspacesStore).workspaces.map((w) => w.id)).toEqual([2]);
    expect(get(workspacesStore).total).toBe(1);
  });

  test('remove of an unknown id leaves the total untouched', () => {
    workspacesStore.add({ id: 1 });
    workspacesStore.remove(99);
    expect(get(workspacesStore).total).toBe(1);
  });

  test('remove is not overwritten by a workspace list request started before deletion', async () => {
    const pending = deferred();
    workspaces.getPage.mockReturnValueOnce(pending.promise);

    const load = workspacesStore.load();
    workspacesStore.add({ id: 1, name: 'Deleted workspace' });
    workspacesStore.remove(1);
    pending.resolve(pageDocument([{ id: 1, name: 'Deleted workspace' }]));
    await load;

    expect(get(workspacesStore).workspaces).toEqual([]);
  });
});

describe('workspacesStore.reload', () => {
  test('clears loaded/loading then reloads from the API', async () => {
    workspaces.getPage
      .mockResolvedValueOnce(pageDocument([{ id: 1 }]))
      .mockResolvedValueOnce(pageDocument([{ id: 1 }, { id: 2 }]));

    await workspacesStore.load();
    expect(get(workspacesStore).workspaces).toHaveLength(1);

    await workspacesStore.reload();
    expect(get(workspacesStore).workspaces).toHaveLength(2);
    expect(workspaces.getPage).toHaveBeenCalledTimes(2);
  });
});

describe('workspacesStore.clear', () => {
  test('drops all state', async () => {
    workspaces.getPage.mockResolvedValueOnce(pageDocument([{ id: 1 }]));
    await workspacesStore.load();
    workspacesStore.clear();

    const state = get(workspacesStore);
    expect(state.workspaces).toEqual([]);
    expect(state.personalWorkspace).toBeNull();
    expect(state.total).toBe(0);
    expect(state.truncated).toBe(false);
    expect(state.loaded).toBe(false);
    expect(state.loading).toBe(false);
  });

  test('does not restore a previous account list after a pending load resolves', async () => {
    const pending = deferred();
    workspaces.getPage.mockReturnValueOnce(pending.promise);

    const load = workspacesStore.load();
    workspacesStore.clear();
    pending.resolve(pageDocument([{ id: 1, name: 'Old account workspace' }]));
    await load;

    const state = get(workspacesStore);
    expect(state.workspaces).toEqual([]);
    expect(state.loaded).toBe(false);
    expect(state.loading).toBe(false);
  });
});
