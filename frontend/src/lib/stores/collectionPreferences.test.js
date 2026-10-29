import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  update: vi.fn(),
}));

vi.mock('../api/users.js', () => ({
  userPreferences: { get: mocks.get, update: mocks.update },
}));

const { collectionPreferences, collectionScopeId } = await import('./collectionPreferences.svelte.js');

beforeEach(() => {
  collectionPreferences.reset();
  vi.clearAllMocks();
  mocks.get.mockResolvedValue({
    view_state: { 'collection-5': { board: { sort_mode: 'bubble' } } },
    saved_filters: { 'collection-5': [{ id: 'a', name: 'Mine', ql: 'id > 0' }] },
  });
  mocks.update.mockResolvedValue({});
});

afterEach(() => {
  collectionPreferences.reset();
});

describe('collectionPreferences store', () => {
  it('hydrates view state and saved filters from the server', async () => {
    await collectionPreferences.ensureLoaded(1);

    expect(collectionPreferences.getViewState(1, 5, 'board')).toEqual({ sort_mode: 'bubble' });
    expect(collectionPreferences.getSavedFilters(1, 5)).toEqual([
      { id: 'a', name: 'Mine', ql: 'id > 0' },
    ]);
    expect(mocks.get).toHaveBeenCalledTimes(1);
  });

  it('returns null and an undefined value for a scope the server does not have', async () => {
    await collectionPreferences.ensureLoaded(1);

    expect(collectionPreferences.getViewState(1, 99, 'board')).toBeNull();
    expect(collectionPreferences.viewStateValue(1, 99, 'board', 'sort_mode')).toBeUndefined();
  });

  it('merges a view patch and flushes only the dirty scope', async () => {
    await collectionPreferences.ensureLoaded(1);

    collectionPreferences.patchViewState(1, 5, 'backlog', { show_completed: true });
    await collectionPreferences.flush();

    expect(mocks.update).toHaveBeenCalledWith({
      view_state: {
        'collection-5': {
          board: { sort_mode: 'bubble' },
          backlog: { show_completed: true },
        },
      },
    });
  });

  it('coalesces a burst of patches into one flush', async () => {
    await collectionPreferences.ensureLoaded(1);

    for (let i = 0; i < 20; i += 1) {
      collectionPreferences.patchViewState(1, 5, 'board', { n: i });
    }
    await collectionPreferences.flush();

    expect(mocks.update).toHaveBeenCalledTimes(1);
  });

  it('marks the save keepalive when flushing for a page unload', async () => {
    await collectionPreferences.ensureLoaded(1);

    collectionPreferences.patchViewState(1, 5, 'board', { sort_mode: 'rank' });
    await collectionPreferences.flush({ keepalive: true });

    expect(mocks.update).toHaveBeenCalledWith(
      { view_state: { 'collection-5': { board: { sort_mode: 'rank' } } } },
      { keepalive: true }
    );
  });

  it('flushes a pending write when the page hides', async () => {
    await collectionPreferences.ensureLoaded(1);

    collectionPreferences.patchViewState(1, 5, 'board', { sort_mode: 'rank' });
    window.dispatchEvent(new Event('pagehide'));

    await vi.waitFor(() => expect(mocks.update).toHaveBeenCalledTimes(1));
    expect(mocks.update).toHaveBeenCalledWith(
      { view_state: { 'collection-5': { board: { sort_mode: 'rank' } } } },
      { keepalive: true }
    );
  });

  it('replaces saved filters for one scope without touching others', async () => {
    await collectionPreferences.ensureLoaded(1);

    collectionPreferences.setSavedFilters(1, 5, [{ id: 'b', name: 'Other', ql: 'id > 1' }]);
    await collectionPreferences.flush();

    expect(mocks.update).toHaveBeenCalledWith({
      saved_filters: { 'collection-5': [{ id: 'b', name: 'Other', ql: 'id > 1' }] },
    });
    expect(collectionPreferences.getSavedFilters(1, 5)).toEqual([
      { id: 'b', name: 'Other', ql: 'id > 1' },
    ]);
  });

  it('re-marks dirty scopes and retries after a failed save', async () => {
    await collectionPreferences.ensureLoaded(1);
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    mocks.update.mockRejectedValueOnce(new Error('offline'));

    collectionPreferences.patchViewState(1, 5, 'board', { sort_mode: 'rank' });
    await collectionPreferences.flush();
    expect(mocks.update).toHaveBeenCalledTimes(1);

    mocks.update.mockResolvedValue({});
    await collectionPreferences.flush();
    warn.mockRestore();

    expect(mocks.update).toHaveBeenCalledTimes(2);
    expect(mocks.update).toHaveBeenLastCalledWith({
      view_state: { 'collection-5': { board: { sort_mode: 'rank' } } },
    });
  });

  it('builds scope ids for collections, workspaces and the global scope', () => {
    expect(collectionScopeId(7, 5)).toBe('collection-5');
    expect(collectionScopeId(7, null)).toBe('workspace-7');
    expect(collectionScopeId(null, null)).toBe('workspace-global');
  });

  it('clears cached state on a user change', async () => {
    await collectionPreferences.ensureLoaded(1);
    expect(collectionPreferences.loaded).toBe(true);

    collectionPreferences.reset();

    expect(collectionPreferences.loaded).toBe(false);
    expect(collectionPreferences.getViewState(1, 5, 'board')).toBeNull();
  });

  it('reloads when a different user is loaded', async () => {
    await collectionPreferences.ensureLoaded(1);
    mocks.get.mockResolvedValue({ view_state: {}, saved_filters: {} });

    await collectionPreferences.ensureLoaded(2);

    expect(mocks.get).toHaveBeenCalledTimes(2);
    expect(collectionPreferences.getViewState(1, 5, 'board')).toBeNull();
  });
});
