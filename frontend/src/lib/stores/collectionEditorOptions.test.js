import { beforeEach, describe, expect, it, vi } from 'vitest';
import { workspaces } from '../api/workspaces.js';
import { getAssignableUsers } from '../api/users.js';
import { iterations } from '../api/milestones.js';
import { priorities } from '../api/configuration.js';
import { customerOrganisations, portalCustomers } from '../api/portal.js';
import { assets } from '../api/assets.js';
import { milestones as milestonesApi } from '../api/milestones.js';








vi.mock('../api/workspaces.js', () => ({
  workspaces: {
        getStatuses: vi.fn(),
        getProjects: vi.fn(),
        get: vi.fn(),
      },
}))

vi.mock('../api/users.js', () => ({
  getAssignableUsers: vi.fn(),
}))

vi.mock('../api/milestones.js', () => ({
  milestones: { getAll: vi.fn() },
  iterations: { getAll: vi.fn() },
  milestoneCategories: { getAll: vi.fn().mockResolvedValue([]) },
}))

vi.mock('../api/configuration.js', () => ({
  priorities: { getAll: vi.fn() },
}))

vi.mock('../api/portal.js', () => ({
  portalCustomers: { getAll: vi.fn() },
  customerOrganisations: { getAll: vi.fn() },
}))

vi.mock('../api/misc.js', () => ({}))

vi.mock('../api/assets.js', () => ({
  assets: { getAll: vi.fn() },
}));


const { CollectionEditorOptionsStore } = await import('./collectionEditorOptions.svelte.js');

describe('CollectionEditorOptionsStore', () => {
  let store;

  beforeEach(() => {
    vi.clearAllMocks();
    store = new CollectionEditorOptionsStore();
  });

  it('does not request options until a picker asks for one family', () => {
    expect(store.get(11).statuses).toEqual([]);
    expect(workspaces.getStatuses).not.toHaveBeenCalled();
    expect(getAssignableUsers).not.toHaveBeenCalled();
    expect(milestonesApi.getAll).not.toHaveBeenCalled();
  });

  it('single-flights repeated loads within one workspace', async () => {
    let resolveRequest;
    getAssignableUsers.mockReturnValue(
      new Promise((resolve) => {
        resolveRequest = resolve;
      })
    );

    const first = store.load(11, 'users');
    const second = store.load(11, 'users');

    expect(getAssignableUsers).toHaveBeenCalledTimes(1);
    expect(getAssignableUsers).toHaveBeenCalledWith(11);

    resolveRequest([{ id: 1, username: 'workspace-eleven' }]);
    await Promise.all([first, second]);

    expect(store.get(11).users).toEqual([{ id: 1, username: 'workspace-eleven' }]);
    expect(store.get(11).loaded.users).toBe(true);
  });

  it('keeps options from mixed-workspace rows isolated', async () => {
    workspaces.getStatuses.mockImplementation(async (workspaceId) => [
      { id: workspaceId * 10, name: `Workspace ${workspaceId}` },
    ]);

    await Promise.all([store.load(11, 'statuses'), store.load(22, 'statuses')]);

    expect(store.get(11).statuses).toEqual([{ id: 110, name: 'Workspace 11' }]);
    expect(store.get(22).statuses).toEqual([{ id: 220, name: 'Workspace 22' }]);
    expect(workspaces.getStatuses).toHaveBeenCalledTimes(2);
  });

  it('resolves priorities through each workspace configuration set', async () => {
    workspaces.get.mockResolvedValue({ id: 11, configuration_set_id: 9 });
    priorities.getAll.mockResolvedValue([{ id: 4, name: 'Configured' }]);

    await store.load(11, 'priorities');

    expect(priorities.getAll).toHaveBeenCalledWith({ configuration_set_id: 9 });
    expect(store.get(11).priorities).toEqual([{ id: 4, name: 'Configured' }]);
  });

  it('falls back to the default priorities when a configuration set has none', async () => {
    const defaultPriorities = [{ id: 5, name: 'Default' }];
    workspaces.get.mockResolvedValue({ id: 11, configuration_set_id: 9 });
    priorities.getAll.mockResolvedValueOnce([]).mockResolvedValueOnce(defaultPriorities);

    await store.load(11, 'priorities');

    expect(priorities.getAll).toHaveBeenNthCalledWith(1, { configuration_set_id: 9 });
    expect(priorities.getAll).toHaveBeenNthCalledWith(2);
    expect(store.get(11).priorities).toEqual(defaultPriorities);
  });

  it('reuses primed workspace-store data without a request', async () => {
    const milestones = [{ id: 7, name: 'Already loaded' }];
    const users = [{ id: 8, username: 'already-loaded' }];
    store.prime(11, { milestones, users }, { milestones: true });

    await Promise.all([store.load(11, 'milestones'), store.load(11, 'users')]);

    expect(store.get(11).milestones).toEqual(milestones);
    expect(store.get(11).users).toEqual(users);
    expect(milestonesApi.getAll).not.toHaveBeenCalled();
    expect(getAssignableUsers).not.toHaveBeenCalled();
  });

  it('refetches a partial planning family as one complete list', async () => {
    const partial = [{ id: 7, name: 'First page' }];
    const complete = [{ id: 7, name: 'First page' }, { id: 8, name: 'Second page' }];
    milestonesApi.getAll.mockResolvedValue(complete);
    store.prime(11, { milestones: partial }, { milestones: false });

    // Partial data is displayable, but a load that needs the directory refetches.
    expect(store.get(11).milestones).toEqual(partial);
    await expect(store.load(11, 'milestones')).resolves.toEqual(complete);

    expect(milestonesApi.getAll).toHaveBeenCalledTimes(1);
    expect(store.get(11).milestones).toEqual(complete);
    expect(store.get(11).complete.milestones).toBe(true);

    // A second load reuses the now-complete, fresh entry.
    await store.load(11, 'milestones');
    expect(milestonesApi.getAll).toHaveBeenCalledTimes(1);
  });

  it('does not let a later partial priming downgrade fresh complete options', async () => {
    const complete = [{ id: 7, name: 'Complete' }];
    store.prime(11, { milestones: complete }, { milestones: true });

    store.prime(11, { milestones: [{ id: 7, name: 'Partial' }] }, { milestones: false });

    expect(store.get(11).milestones).toEqual(complete);
    expect(store.get(11).complete.milestones).toBe(true);
  });

  it('keeps the primed entry identity when an effect primes the same partial list again', () => {
    // Regression: priming from an effect re-reads the store, so an identical
    // write sent Svelte into effect_update_depth_exceeded on the hierarchy view.
    const milestones = [{ id: 7 }, { id: 8 }];
    const filtered = () => milestones.filter(() => true);

    store.prime(11, { milestones: filtered() }, { milestones: false });
    const primedEntry = store.byWorkspace;

    store.prime(11, { milestones: filtered() }, { milestones: false });

    expect(store.byWorkspace).toBe(primedEntry);
    expect(store.get(11).milestones).toEqual(milestones);
    expect(store.get(11).complete.milestones).toBe(false);
  });

  it('replaces a primed partial list when a complete list arrives', () => {
    const partial = [{ id: 7 }];
    const complete = [{ id: 7 }, { id: 8 }];
    store.prime(11, { milestones: partial }, { milestones: false });
    const primedEntry = store.byWorkspace;

    store.prime(11, { milestones: complete }, { milestones: true });

    expect(store.byWorkspace).not.toBe(primedEntry);
    expect(store.get(11).milestones).toEqual(complete);
    expect(store.get(11).complete.milestones).toBe(true);
  });

  it('treats missing completeness metadata as an incomplete planning list', async () => {
    const primed = [{ id: 7, name: 'Unknown completeness' }];
    milestonesApi.getAll.mockResolvedValue([{ id: 7, name: 'Reloaded' }]);
    store.prime(11, { milestones: primed });

    await store.load(11, 'milestones');

    expect(milestonesApi.getAll).toHaveBeenCalledTimes(1);
    expect(store.get(11).milestones).toEqual([{ id: 7, name: 'Reloaded' }]);
  });

  it('single-flights asset options by workspace, set, filter, and search', async () => {
    assets.getAll.mockResolvedValue({
      data: [{ id: 3 }],
      pagination: { total_items: 1 },
    });

    const [first, second] = await Promise.all([
      store.loadAssets(11, 4, 'status = active', 'lap'),
      store.loadAssets(11, 4, 'status = active', 'lap'),
    ]);

    expect(first).toEqual({ assets: [{ id: 3 }], total: 1 });
    expect(second).toEqual(first);
    expect(assets.getAll).toHaveBeenCalledTimes(1);
    expect(assets.getAll).toHaveBeenCalledWith(4, {
      cql: 'status = active',
      search: 'lap',
    });
  });

  it('invalidates one option family across every workspace', async () => {
    milestonesApi.getAll.mockResolvedValue([{ id: 1, name: 'M' }]);
    iterations.getAll.mockResolvedValue([{ id: 2, name: 'I' }]);

    await Promise.all([
      store.load(11, 'milestones'),
      store.load(22, 'milestones'),
      store.load(11, 'iterations'),
    ]);

    store.invalidateFamily('milestones');

    expect(store.get(11).milestones).toEqual([]);
    expect(store.get(22).milestones).toEqual([]);
    expect(store.get(11).complete.milestones).toBe(false);
    expect(store.get(11).iterations).toEqual([{ id: 2, name: 'I' }]);
  });

  it('does not restore previous account options after reset', async () => {
    let resolveOldRequest;
    getAssignableUsers.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveOldRequest = resolve;
      })
    );

    const oldLoad = store.load(11, 'users');
    store.reset();
    resolveOldRequest([{ id: 9, username: 'previous-account-user' }]);
    await oldLoad;

    expect(store.get(11).users).toEqual([]);
    expect(store.get(11).loaded.users).not.toBe(true);
  });
});
