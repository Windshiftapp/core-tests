import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api.js', () => ({
  api: {
    workspaces: {
      getStatuses: vi.fn(),
      getProjects: vi.fn(),
      get: vi.fn(),
    },
    getAssignableUsers: vi.fn(),
    milestones: { getAll: vi.fn() },
    iterations: { getAll: vi.fn() },
    priorities: { getAll: vi.fn() },
    portalCustomers: { getAll: vi.fn() },
    customerOrganisations: { getAll: vi.fn() },
    personalLabels: { getAll: vi.fn() },
    assets: { getAll: vi.fn() },
  },
}));

const { api } = await import('../api.js');
const { CollectionEditorOptionsStore } = await import('./collectionEditorOptions.svelte.js');

describe('CollectionEditorOptionsStore', () => {
  let store;

  beforeEach(() => {
    vi.clearAllMocks();
    store = new CollectionEditorOptionsStore();
  });

  it('does not request options until a picker asks for one family', () => {
    expect(store.get(11).statuses).toEqual([]);
    expect(api.workspaces.getStatuses).not.toHaveBeenCalled();
    expect(api.getAssignableUsers).not.toHaveBeenCalled();
    expect(api.milestones.getAll).not.toHaveBeenCalled();
  });

  it('single-flights repeated loads within one workspace', async () => {
    let resolveRequest;
    api.getAssignableUsers.mockReturnValue(
      new Promise((resolve) => {
        resolveRequest = resolve;
      })
    );

    const first = store.load(11, 'users');
    const second = store.load(11, 'users');

    expect(api.getAssignableUsers).toHaveBeenCalledTimes(1);
    expect(api.getAssignableUsers).toHaveBeenCalledWith(11);

    resolveRequest([{ id: 1, username: 'workspace-eleven' }]);
    await Promise.all([first, second]);

    expect(store.get(11).users).toEqual([{ id: 1, username: 'workspace-eleven' }]);
    expect(store.get(11).loaded.users).toBe(true);
  });

  it('keeps options from mixed-workspace rows isolated', async () => {
    api.workspaces.getStatuses.mockImplementation(async (workspaceId) => [
      { id: workspaceId * 10, name: `Workspace ${workspaceId}` },
    ]);

    await Promise.all([store.load(11, 'statuses'), store.load(22, 'statuses')]);

    expect(store.get(11).statuses).toEqual([{ id: 110, name: 'Workspace 11' }]);
    expect(store.get(22).statuses).toEqual([{ id: 220, name: 'Workspace 22' }]);
    expect(api.workspaces.getStatuses).toHaveBeenCalledTimes(2);
  });

  it('resolves priorities through each workspace configuration set', async () => {
    api.workspaces.get.mockResolvedValue({ id: 11, configuration_set_id: 9 });
    api.priorities.getAll.mockResolvedValue([{ id: 4, name: 'Configured' }]);

    await store.load(11, 'priorities');

    expect(api.priorities.getAll).toHaveBeenCalledWith({ configuration_set_id: 9 });
    expect(store.get(11).priorities).toEqual([{ id: 4, name: 'Configured' }]);
  });

  it('falls back to the default priorities when a configuration set has none', async () => {
    const defaultPriorities = [{ id: 5, name: 'Default' }];
    api.workspaces.get.mockResolvedValue({ id: 11, configuration_set_id: 9 });
    api.priorities.getAll.mockResolvedValueOnce([]).mockResolvedValueOnce(defaultPriorities);

    await store.load(11, 'priorities');

    expect(api.priorities.getAll).toHaveBeenNthCalledWith(1, { configuration_set_id: 9 });
    expect(api.priorities.getAll).toHaveBeenNthCalledWith(2);
    expect(store.get(11).priorities).toEqual(defaultPriorities);
  });

  it('reuses primed workspace-store data without a request', async () => {
    const milestones = [{ id: 7, name: 'Already loaded' }];
    const users = [{ id: 8, username: 'already-loaded' }];
    store.prime(11, { milestones, users });

    await Promise.all([store.load(11, 'milestones'), store.load(11, 'users')]);

    expect(store.get(11).milestones).toEqual(milestones);
    expect(store.get(11).users).toEqual(users);
    expect(api.milestones.getAll).not.toHaveBeenCalled();
    expect(api.getAssignableUsers).not.toHaveBeenCalled();
  });

  it('single-flights asset options by workspace, set, filter, and search', async () => {
    api.assets.getAll.mockResolvedValue({ assets: [{ id: 3 }], total: 1 });

    const [first, second] = await Promise.all([
      store.loadAssets(11, 4, 'status = active', 'lap'),
      store.loadAssets(11, 4, 'status = active', 'lap'),
    ]);

    expect(first).toEqual({ assets: [{ id: 3 }], total: 1 });
    expect(second).toEqual(first);
    expect(api.assets.getAll).toHaveBeenCalledTimes(1);
    expect(api.assets.getAll).toHaveBeenCalledWith(4, {
      cql: 'status = active',
      search: 'lap',
    });
  });

  it('does not restore previous account options after reset', async () => {
    let resolveOldRequest;
    api.getAssignableUsers.mockReturnValueOnce(
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
