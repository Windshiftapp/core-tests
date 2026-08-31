import { get } from 'svelte/store';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

// Mock the api module before importing the store — the store calls
// api.workspaces.* at runtime.
vi.mock('../api.js', () => ({
  api: {
    workspaces: {
      get: vi.fn(),
      getAll: vi.fn(),
      getOrCreatePersonal: vi.fn(),
    },
  },
}));

import { api } from '../api.js';
import { currentWorkspace, workspacesStore } from './workspaces.svelte.js';

function deferred() {
  let resolve;
  const promise = new Promise((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
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

    expect(api.workspaces.get).not.toHaveBeenCalled();
    expect(get(currentWorkspace)).toEqual(workspace);
  });

  test('fetches the workspace and stores it', async () => {
    const ws = { id: 1, name: 'Alpha' };
    api.workspaces.get.mockResolvedValueOnce(ws);

    await currentWorkspace.load(1);

    expect(api.workspaces.get).toHaveBeenCalledWith(1);
    expect(get(currentWorkspace)).toEqual(ws);
  });

  test('clears state when called without an id', async () => {
    api.workspaces.get.mockResolvedValueOnce({ id: 1 });
    await currentWorkspace.load(1);

    await currentWorkspace.load(null);

    expect(get(currentWorkspace)).toBeNull();
  });

  test('deduplicates back-to-back calls with the same id', async () => {
    api.workspaces.get.mockResolvedValueOnce({ id: 1 });
    await currentWorkspace.load(1);
    await currentWorkspace.load(1);
    expect(api.workspaces.get).toHaveBeenCalledTimes(1);
  });

  test('reloads when the id changes', async () => {
    api.workspaces.get
      .mockResolvedValueOnce({ id: 1, name: 'A' })
      .mockResolvedValueOnce({ id: 2, name: 'B' });
    await currentWorkspace.load(1);
    await currentWorkspace.load(2);
    expect(api.workspaces.get).toHaveBeenCalledTimes(2);
    expect(get(currentWorkspace)).toEqual({ id: 2, name: 'B' });
  });

  test('does not let a slower previous workspace replace the current route', async () => {
    const first = deferred();
    const second = deferred();
    api.workspaces.get.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);

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
    api.workspaces.get.mockRejectedValueOnce(new Error('500'));
    await currentWorkspace.load(7);
    expect(get(currentWorkspace)).toBeNull();
    expect(errSpy).toHaveBeenCalled();
  });

  test('after a failed load, retrying with the same id re-fetches (no stale dedupe)', async () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    api.workspaces.get.mockRejectedValueOnce(new Error('500'));
    await currentWorkspace.load(7);
    expect(errSpy).toHaveBeenCalled();

    // lastWorkspaceId is only set on a successful fetch, so the second call
    // with the same id retries instead of being deduped against the failed one.
    api.workspaces.get.mockResolvedValueOnce({ id: 7 });
    await currentWorkspace.load(7);
    expect(api.workspaces.get).toHaveBeenCalledTimes(2);
    expect(get(currentWorkspace)).toEqual({ id: 7 });
  });

  test('can return to the previous workspace after a different workspace fails to load', async () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    api.workspaces.get
      .mockResolvedValueOnce({ id: 1, name: 'A' })
      .mockRejectedValueOnce(new Error('workspace B unavailable'))
      .mockResolvedValueOnce({ id: 1, name: 'A reloaded' });

    await currentWorkspace.load(1);
    await currentWorkspace.load(2);
    expect(errSpy).toHaveBeenCalled();
    expect(get(currentWorkspace)).toBeNull();

    await currentWorkspace.load(1);

    expect(api.workspaces.get).toHaveBeenCalledTimes(3);
    expect(get(currentWorkspace)).toEqual({ id: 1, name: 'A reloaded' });
  });
});

describe('currentWorkspace.patch', () => {
  test('merges partial updates onto the current workspace', async () => {
    api.workspaces.get.mockResolvedValueOnce({ id: 1, name: 'A', key: 'AAA' });
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
    api.workspaces.get
      .mockResolvedValueOnce({ id: 1, name: 'A' })
      .mockResolvedValueOnce({ id: 1, name: 'A reloaded' });

    await currentWorkspace.load(1);
    currentWorkspace.clear();
    await currentWorkspace.load(1);

    expect(api.workspaces.get).toHaveBeenCalledTimes(2);
    expect(get(currentWorkspace)).toEqual({ id: 1, name: 'A reloaded' });
  });

  test('does not restore a workspace from a request that resolves after clear', async () => {
    const pending = deferred();
    api.workspaces.get.mockReturnValueOnce(pending.promise);

    const load = currentWorkspace.load(1);
    currentWorkspace.clear();
    pending.resolve({ id: 1, name: 'Old account workspace' });
    await load;

    expect(get(currentWorkspace)).toBeNull();
  });
});

describe('workspacesStore.load', () => {
  test('populates workspaces from the API', async () => {
    api.workspaces.getAll.mockResolvedValueOnce([
      { id: 1, name: 'A' },
      { id: 2, name: 'B', is_personal: true },
    ]);

    await workspacesStore.load();

    const state = get(workspacesStore);
    expect(state.workspaces).toHaveLength(2);
    expect(state.loaded).toBe(true);
    expect(state.loading).toBe(false);
  });

  test('falls back to empty array when API returns null', async () => {
    api.workspaces.getAll.mockResolvedValueOnce(null);
    await workspacesStore.load();
    expect(get(workspacesStore).workspaces).toEqual([]);
    expect(get(workspacesStore).loaded).toBe(true);
  });

  test('on API failure: empty list, loaded=true, error logged', async () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    api.workspaces.getAll.mockRejectedValueOnce(new Error('500'));
    await workspacesStore.load();
    expect(get(workspacesStore).workspaces).toEqual([]);
    expect(get(workspacesStore).loaded).toBe(true);
    expect(get(workspacesStore).loading).toBe(false);
    expect(errSpy).toHaveBeenCalled();
  });

  test('shares an in-flight list request and reuses the loaded catalog', async () => {
    const pending = deferred();
    api.workspaces.getAll.mockReturnValueOnce(pending.promise);

    const first = workspacesStore.load();
    const second = workspacesStore.load();
    expect(api.workspaces.getAll).toHaveBeenCalledOnce();

    const all = [{ id: 1, name: 'Shared' }];
    pending.resolve(all);
    await expect(Promise.all([first, second])).resolves.toEqual([all, all]);
    await expect(workspacesStore.load()).resolves.toEqual(all);
    expect(api.workspaces.getAll).toHaveBeenCalledOnce();
  });
});

describe('workspacesStore — regularWorkspaces derived', () => {
  test('filters out personal workspaces', async () => {
    api.workspaces.getAll.mockResolvedValueOnce([
      { id: 1, name: 'A', is_personal: false },
      { id: 2, name: 'B', is_personal: true },
      { id: 3, name: 'C' }, // no flag = regular
    ]);
    await workspacesStore.load();

    const ids = get(workspacesStore).regularWorkspaces.map((w) => w.id);
    expect(ids).toEqual([1, 3]);
  });
});

describe('workspacesStore.loadPersonalWorkspace', () => {
  test('stores and returns the personal workspace', async () => {
    const personal = { id: 99, is_personal: true };
    api.workspaces.getOrCreatePersonal.mockResolvedValueOnce(personal);

    const result = await workspacesStore.loadPersonalWorkspace();

    expect(result).toEqual(personal);
    expect(get(workspacesStore).personalWorkspace).toEqual(personal);
  });

  test('returns null and logs on API failure', async () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    api.workspaces.getOrCreatePersonal.mockRejectedValueOnce(new Error('boom'));
    const result = await workspacesStore.loadPersonalWorkspace();
    expect(result).toBeNull();
    expect(get(workspacesStore).personalWorkspace).toBeNull();
    expect(errSpy).toHaveBeenCalled();
  });

  test('shares an in-flight personal workspace request and reuses its result', async () => {
    const pending = deferred();
    api.workspaces.getOrCreatePersonal.mockReturnValueOnce(pending.promise);

    const first = workspacesStore.loadPersonalWorkspace();
    const second = workspacesStore.loadPersonalWorkspace();
    expect(api.workspaces.getOrCreatePersonal).toHaveBeenCalledOnce();

    const personal = { id: 99, is_personal: true };
    pending.resolve(personal);
    await expect(Promise.all([first, second])).resolves.toEqual([personal, personal]);
    await expect(workspacesStore.loadPersonalWorkspace()).resolves.toBe(personal);
    expect(api.workspaces.getOrCreatePersonal).toHaveBeenCalledOnce();
  });
});

describe('workspacesStore CRUD-style mutations', () => {
  test('add appends a workspace', () => {
    workspacesStore.add({ id: 1, name: 'A' });
    workspacesStore.add({ id: 2, name: 'B' });
    expect(get(workspacesStore).workspaces.map((w) => w.id)).toEqual([1, 2]);
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

  test('remove drops a workspace by id', () => {
    workspacesStore.add({ id: 1 });
    workspacesStore.add({ id: 2 });
    workspacesStore.remove(1);
    expect(get(workspacesStore).workspaces.map((w) => w.id)).toEqual([2]);
  });

  test('remove is not overwritten by a workspace list request started before deletion', async () => {
    const pending = deferred();
    api.workspaces.getAll.mockReturnValueOnce(pending.promise);

    const load = workspacesStore.load();
    workspacesStore.add({ id: 1, name: 'Deleted workspace' });
    workspacesStore.remove(1);
    pending.resolve([{ id: 1, name: 'Deleted workspace' }]);
    await load;

    expect(get(workspacesStore).workspaces).toEqual([]);
  });
});

describe('workspacesStore.reload', () => {
  test('clears loaded/loading then reloads from the API', async () => {
    api.workspaces.getAll
      .mockResolvedValueOnce([{ id: 1 }])
      .mockResolvedValueOnce([{ id: 1 }, { id: 2 }]);

    await workspacesStore.load();
    expect(get(workspacesStore).workspaces).toHaveLength(1);

    await workspacesStore.reload();
    expect(get(workspacesStore).workspaces).toHaveLength(2);
    expect(api.workspaces.getAll).toHaveBeenCalledTimes(2);
  });
});

describe('workspacesStore.clear', () => {
  test('drops all state', async () => {
    api.workspaces.getAll.mockResolvedValueOnce([{ id: 1 }]);
    await workspacesStore.load();
    workspacesStore.clear();

    const state = get(workspacesStore);
    expect(state.workspaces).toEqual([]);
    expect(state.personalWorkspace).toBeNull();
    expect(state.loaded).toBe(false);
    expect(state.loading).toBe(false);
  });

  test('does not restore a previous account list after a pending load resolves', async () => {
    const pending = deferred();
    api.workspaces.getAll.mockReturnValueOnce(pending.promise);

    const load = workspacesStore.load();
    workspacesStore.clear();
    pending.resolve([{ id: 1, name: 'Old account workspace' }]);
    await load;

    const state = get(workspacesStore);
    expect(state.workspaces).toEqual([]);
    expect(state.loaded).toBe(false);
    expect(state.loading).toBe(false);
  });
});
