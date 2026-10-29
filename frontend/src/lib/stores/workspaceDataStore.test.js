import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { workspaces } from '../api/workspaces.js';
import { customFields, itemTypes, priorities } from '../api/configuration.js';
import { statusCategories, statuses } from '../api/workflows.js';
import { getAssignableUsers, getUserDisplayBatch, getUsers } from '../api/users.js';
import { iterations, milestones } from '../api/milestones.js';





const mocks = vi.hoisted(() => ({
  getBootstrap: vi.fn(),
  getWorkspace: vi.fn(),
  getStatuses: vi.fn(),
  getProjects: vi.fn(),
  getAssignableUsers: vi.fn(),
  getUsers: vi.fn(),
  getUserDisplayBatch: vi.fn(),
  getItemTypes: vi.fn(),
  getStatusCategories: vi.fn(),
  getMilestones: vi.fn(),
  getIterations: vi.fn(),
  getPriorities: vi.fn(),
  getCustomFields: vi.fn(),
  getGlobalBootstrap: vi.fn(),
  getEffectiveConfig: vi.fn(),
  getLabels: vi.fn(),
}));

vi.mock('../api/workspaces.js', () => ({
  workspaces: {
        getBootstrap: mocks.getBootstrap,
        get: mocks.getWorkspace,
        getStatuses: mocks.getStatuses,
        getProjects: mocks.getProjects,
        getEffectiveConfig: mocks.getEffectiveConfig,
      },
}))

vi.mock('../api/configuration.js', () => ({
  itemTypes: { getAll: mocks.getItemTypes },
  priorities: { getAll: mocks.getPriorities },
  customFields: { getAll: mocks.getCustomFields },
  catalogs: { getGlobalBootstrap: mocks.getGlobalBootstrap },
}))

vi.mock('../api/workflows.js', () => ({
  statuses: { getAll: mocks.getStatuses },
  statusCategories: { getAll: mocks.getStatusCategories },
}))

vi.mock('../api/users.js', () => ({
  getAssignableUsers: mocks.getAssignableUsers,
  getUsers: mocks.getUsers,
  getUserDisplayBatch: mocks.getUserDisplayBatch,
}))

vi.mock('../api/milestones.js', () => ({
  milestones: { getAll: mocks.getMilestones },
  iterations: { getAll: mocks.getIterations },
  milestoneCategories: { getAll: vi.fn().mockResolvedValue([]) },
}));

vi.mock('../api/misc.js', () => ({
  labels: { getAll: mocks.getLabels },
}));

const { workspaceDataStore } = await import('./workspaceDataStore.svelte.js');

function bootstrap(workspace, overrides = {}) {
  return {
    workspace,
    homepage_layout: { sections: [], widgets: [], gradient: 2 },
    statuses: [],
    status_categories: [],
    item_types: [],
    users: [],
    milestones: [],
    iterations: [],
    priorities: [],
    projects: [],
    custom_field_definitions: [],
    ...overrides,
  };
}

describe('WorkspaceDataStore workspace switching', () => {
  beforeEach(() => {
    workspaceDataStore.reset();
    vi.clearAllMocks();
    mocks.getBootstrap.mockReset();
    mocks.getWorkspace.mockReset();
    mocks.getLabels.mockReset();
    mocks.getLabels.mockResolvedValue([]);
  });

  afterEach(() => {
    workspaceDataStore.reset();
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('synchronously clears reference data from the previous workspace', async () => {
    workspaceDataStore.workspaceId = 1;
    workspaceDataStore.workspace = { id: 1, name: 'Previous workspace' };
    workspaceDataStore.statuses = [{ id: 10, name: 'Previous status' }];
    workspaceDataStore.itemTypes = [{ id: 20, name: 'Previous type' }];
    workspaceDataStore.initialized = true;

    let resolveWorkspace;
    mocks.getBootstrap.mockReturnValue(
      new Promise((resolve) => {
        resolveWorkspace = resolve;
      })
    );

    const initialization = workspaceDataStore.initialize(2);

    expect(workspaceDataStore.workspaceId).toBe(2);
    expect(workspaceDataStore.workspace).toBeNull();
    expect(workspaceDataStore.statuses).toEqual([]);
    expect(workspaceDataStore.itemTypes).toEqual([]);
    expect(workspaceDataStore.initialLoading).toBe(true);

    resolveWorkspace(bootstrap({ id: 2, name: 'Current workspace' }));
    await initialization;

    expect(workspaceDataStore.workspace).toEqual({ id: 2, name: 'Current workspace' });
    expect(workspaceDataStore.initialized).toBe(true);
  });

  it('keeps the current workspace initialization single-flighted after a stale load settles', async () => {
    let resolveFirstWorkspace;
    let resolveSecondWorkspace;
    mocks.getBootstrap
      .mockReturnValueOnce(
        new Promise((resolve) => {
          resolveFirstWorkspace = resolve;
        })
      )
      .mockReturnValueOnce(
        new Promise((resolve) => {
          resolveSecondWorkspace = resolve;
        })
      );

    const firstInitialization = workspaceDataStore.initialize(1);
    const secondInitialization = workspaceDataStore.initialize(2);
    resolveFirstWorkspace(bootstrap({ id: 1, name: 'Stale workspace' }));
    await firstInitialization;

    const duplicateInitialization = workspaceDataStore.initialize(2);
    expect(mocks.getBootstrap).toHaveBeenCalledTimes(2);

    resolveSecondWorkspace(bootstrap({ id: 2, name: 'Current workspace' }));
    await Promise.all([secondInitialization, duplicateInitialization]);
    expect(workspaceDataStore.workspace).toEqual({ id: 2, name: 'Current workspace' });
  });

  it('skips hidden-tab intervals and refreshes when the tab returns', async () => {
    vi.useFakeTimers();
    let visibilityState = 'visible';
    vi.spyOn(document, 'hidden', 'get').mockImplementation(() => visibilityState === 'hidden');
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibilityState);
    mocks.getBootstrap.mockResolvedValue(bootstrap({ id: 2, name: 'Workspace' }));

    await workspaceDataStore.initialize(2);
    mocks.getBootstrap.mockClear();
    visibilityState = 'hidden';
    // Browsers always deliver this event when a tab is hidden; the recovery
    // refresh depends on observing the suspension.
    document.dispatchEvent(new Event('visibilitychange'));

    await vi.advanceTimersByTimeAsync(5 * 60 * 1000);
    expect(mocks.getBootstrap).not.toHaveBeenCalled();

    visibilityState = 'visible';
    document.dispatchEvent(new Event('visibilitychange'));
    expect(mocks.getBootstrap).toHaveBeenCalledTimes(1);
  });

  it('does not warn for expected background connectivity failures', async () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
    mocks.getBootstrap.mockResolvedValueOnce(bootstrap({ id: 2, name: 'Workspace' }));
    await workspaceDataStore.initialize(2);
    mocks.getBootstrap.mockRejectedValueOnce(
      Object.assign(new Error('offline'), { code: 'NETWORK_ERROR' })
    );

    await workspaceDataStore.refresh();

    expect(warnSpy).not.toHaveBeenCalled();
  });

  it('hydrates every workspace reference from one aggregate request', async () => {
    mocks.getBootstrap.mockResolvedValue(
      bootstrap(
        { id: 9, name: 'Aggregate workspace' },
        {
          statuses: [{ id: 1, name: 'Open' }],
          status_categories: [{ id: 2, name: 'To do' }],
          item_types: [{ id: 3, name: 'Task' }],
          users: [{ id: 4, username: 'alex' }],
          milestones: [{ id: 5, name: 'Launch' }],
          iterations: [{ id: 6, name: 'Sprint' }],
          priorities: [{ id: 7, name: 'High' }],
          projects: [{ id: 8, name: 'Delivery' }],
          custom_field_definitions: [{ id: 10, name: 'Impact' }],
        }
      )
    );

    await workspaceDataStore.initialize(9);

    expect(mocks.getBootstrap).toHaveBeenCalledOnce();
    expect(workspaceDataStore.homepageLayout.gradient).toBe(2);
    expect(workspaceDataStore.statuses[0].name).toBe('Open');
    expect(workspaceDataStore.itemTypes[0].name).toBe('Task');
    expect(workspaceDataStore.customFieldDefinitions[0].name).toBe('Impact');
    expect(mocks.getWorkspace).not.toHaveBeenCalled();
    expect(mocks.getStatuses).not.toHaveBeenCalled();
    expect(mocks.getProjects).not.toHaveBeenCalled();
    expect(mocks.getAssignableUsers).not.toHaveBeenCalled();
    expect(mocks.getItemTypes).not.toHaveBeenCalled();
    expect(mocks.getStatusCategories).not.toHaveBeenCalled();
    expect(mocks.getMilestones).not.toHaveBeenCalled();
    expect(mocks.getIterations).not.toHaveBeenCalled();
    expect(mocks.getPriorities).not.toHaveBeenCalled();
    expect(mocks.getCustomFields).not.toHaveBeenCalled();
  });

  it('loads the workspace label directory alongside the bootstrap', async () => {
    mocks.getBootstrap.mockResolvedValue(bootstrap({ id: 9, name: 'Labels' }));
    mocks.getLabels.mockResolvedValue([{ id: 1, name: 'urgent', color: '#f00' }]);

    await workspaceDataStore.initialize(9);

    expect(mocks.getLabels).toHaveBeenCalledWith(9);
    expect(workspaceDataStore.labels).toEqual([{ id: 1, name: 'urgent', color: '#f00' }]);
  });

  it('keeps labels empty and warns when the label directory fails', async () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
    mocks.getBootstrap.mockResolvedValue(bootstrap({ id: 9, name: 'Labels' }));
    mocks.getLabels.mockRejectedValue(new Error('boom'));

    await workspaceDataStore.initialize(9);

    expect(workspaceDataStore.labels).toEqual([]);
    expect(warnSpy).toHaveBeenCalledWith(
      'WorkspaceDataStore: failed to load labels',
      expect.any(Error)
    );
  });

  it('refreshes labels through a granular invalidation', async () => {
    mocks.getBootstrap.mockResolvedValue(bootstrap({ id: 9, name: 'Labels' }));
    mocks.getLabels.mockResolvedValueOnce([{ id: 1, name: 'old' }]);
    await workspaceDataStore.initialize(9);

    mocks.getLabels.mockResolvedValueOnce([{ id: 2, name: 'new' }]);
    await workspaceDataStore.invalidate('labels');

    expect(workspaceDataStore.labels).toEqual([{ id: 2, name: 'new' }]);
  });

  it('exposes bootstrap planning completeness and treats missing metadata as unknown', async () => {
    mocks.getBootstrap.mockResolvedValue(
      bootstrap(
        { id: 9, name: 'Aggregate workspace' },
        { planning_completeness: { milestones: false, iterations: true } }
      )
    );

    await workspaceDataStore.initialize(9);
    expect(workspaceDataStore.planningCompleteness).toEqual({
      milestones: false,
      iterations: true,
    });

    workspaceDataStore.reset();
    mocks.getBootstrap.mockResolvedValue(bootstrap({ id: 10, name: 'Legacy workspace' }));
    await workspaceDataStore.initialize(10);

    // Absent metadata means unknown, never complete.
    expect(workspaceDataStore.planningCompleteness).toBeNull();
  });

  it('returns the active scope references without a bootstrap request', async () => {
    mocks.getBootstrap.mockResolvedValue(
      bootstrap({ id: 9, name: 'Active' }, { milestones: [{ id: 5 }] })
    );
    await workspaceDataStore.initialize(9);
    mocks.getBootstrap.mockClear();

    const snapshot = await workspaceDataStore.getReferenceSnapshot(9);

    expect(mocks.getBootstrap).not.toHaveBeenCalled();
    expect(snapshot.workspace).toEqual({ id: 9, name: 'Active' });
    expect(snapshot.milestones).toEqual([{ id: 5 }]);
  });

  it('deduplicates a bootstrap for a non-active workspace without activating it', async () => {
    workspaceDataStore.workspaceId = 'global';
    workspaceDataStore.initialized = true;
    let resolveBootstrap;
    mocks.getBootstrap.mockReturnValue(
      new Promise((resolve) => {
        resolveBootstrap = resolve;
      })
    );

    const first = workspaceDataStore.getReferenceSnapshot(9);
    const second = workspaceDataStore.getReferenceSnapshot(9);
    expect(mocks.getBootstrap).toHaveBeenCalledTimes(1);

    resolveBootstrap(bootstrap({ id: 9, name: 'Other' }, { milestones: [{ id: 6 }] }));
    await expect(first).resolves.toMatchObject({ workspace: { id: 9 } });
    await expect(second).resolves.toMatchObject({ milestones: [{ id: 6 }] });
    // The global scope is still active; the requested workspace was not activated.
    expect(workspaceDataStore.workspaceId).toBe('global');
  });
});

describe('WorkspaceDataStore effective-config cache', () => {
  beforeEach(() => {
    workspaceDataStore.reset();
    vi.clearAllMocks();
    mocks.getEffectiveConfig.mockReset();
  });

  afterEach(() => {
    workspaceDataStore.reset();
  });

  it('caches effective config per workspace and item type', async () => {
    mocks.getEffectiveConfig.mockResolvedValue({ edit_screen: { id: 1, fields: [] } });

    await workspaceDataStore.screenConfigFor(1, 5);
    await workspaceDataStore.screenConfigFor(1, 6);
    await workspaceDataStore.screenConfigFor(2, 5);

    expect(Object.keys(workspaceDataStore.screenConfigs).sort()).toEqual([
      '1:5',
      '1:6',
      '2:5',
    ]);
    expect(mocks.getEffectiveConfig).toHaveBeenCalledTimes(3);
  });

  it('keys a null item type as the default configuration', async () => {
    mocks.getEffectiveConfig.mockResolvedValue({ edit_screen: { id: 1, fields: [] } });

    await workspaceDataStore.screenConfigFor(4, null);

    expect(workspaceDataStore.screenConfigs['4:default']).toBeDefined();
  });

  it('shares one in-flight request per cache key', async () => {
    let resolveConfig;
    mocks.getEffectiveConfig.mockReturnValue(
      new Promise((resolve) => {
        resolveConfig = resolve;
      })
    );

    const first = workspaceDataStore.screenConfigFor(1, 5);
    const second = workspaceDataStore.screenConfigFor(1, 5);
    expect(mocks.getEffectiveConfig).toHaveBeenCalledTimes(1);

    resolveConfig({ edit_screen: { id: 1, fields: [] } });
    await expect(first).resolves.toMatchObject({ edit_screen: { id: 1 } });
    await expect(second).resolves.toMatchObject({ edit_screen: { id: 1 } });
  });

  it('serves a cached configuration without a second request', async () => {
    mocks.getEffectiveConfig.mockResolvedValue({ edit_screen: { id: 1, fields: [] } });

    await workspaceDataStore.screenConfigFor(1, 5);
    await workspaceDataStore.screenConfigFor(1, 5);

    expect(mocks.getEffectiveConfig).toHaveBeenCalledTimes(1);
  });

  it('does not cache a response that settles after invalidation', async () => {
    let resolveConfig;
    mocks.getEffectiveConfig.mockReturnValue(
      new Promise((resolve) => {
        resolveConfig = resolve;
      })
    );

    const pending = workspaceDataStore.screenConfigFor(1, 5);
    await Promise.resolve();
    workspaceDataStore.invalidateScreenConfigs();
    resolveConfig({ edit_screen: { id: 1, fields: [] } });
    await pending;

    expect(workspaceDataStore.screenConfigs['1:5']).toBeUndefined();
  });

  it('drops cached and pending configurations on invalidation', async () => {
    mocks.getEffectiveConfig.mockResolvedValue({ edit_screen: { id: 1, fields: [] } });
    await workspaceDataStore.screenConfigFor(1, 5);
    expect(workspaceDataStore.screenConfigs['1:5']).toBeDefined();

    workspaceDataStore.invalidateScreenConfigs();

    expect(workspaceDataStore.screenConfigs).toEqual({});
    mocks.getEffectiveConfig.mockClear();
    await workspaceDataStore.screenConfigFor(1, 5);
    expect(mocks.getEffectiveConfig).toHaveBeenCalledTimes(1);
  });

  it('bounds the cache and evicts the least recently used entry', async () => {
    mocks.getEffectiveConfig.mockResolvedValue({ edit_screen: { id: 1, fields: [] } });

    for (let workspaceId = 1; workspaceId <= 51; workspaceId += 1) {
      await workspaceDataStore.screenConfigFor(workspaceId, 1);
    }

    const keys = Object.keys(workspaceDataStore.screenConfigs);
    expect(keys).toHaveLength(50);
    expect(keys).not.toContain('1:1');
    expect(keys).toContain('51:1');
  });

  it('skips the request for a missing or global workspace scope', async () => {
    await expect(workspaceDataStore.screenConfigFor(null, 5)).resolves.toBeNull();
    await expect(workspaceDataStore.screenConfigFor('global', 5)).resolves.toBeNull();
    expect(mocks.getEffectiveConfig).not.toHaveBeenCalled();
  });

  it('resolves the current workspace through the screenConfig delegate', async () => {
    workspaceDataStore.workspaceId = 3;
    mocks.getEffectiveConfig.mockResolvedValue({ edit_screen: { id: 1, fields: [] } });

    await workspaceDataStore.screenConfig(7);

    expect(mocks.getEffectiveConfig).toHaveBeenCalledWith(3, 7, expect.anything());
    expect(workspaceDataStore.screenConfigs['3:7']).toBeDefined();
  });
});

describe('WorkspaceDataStore global user hydration (WI-1621, WI-1743)', () => {
  beforeEach(() => {
    workspaceDataStore.reset();
    vi.clearAllMocks();
    mocks.getGlobalBootstrap.mockResolvedValue({
      item_types: [],
      statuses: [],
      status_categories: [],
      priorities: [],
      custom_field_definitions: [],
      custom_fields_error: null,
    });
    mocks.getUsers.mockResolvedValue([{ id: 99, username: 'roster-user' }]);
    mocks.getUserDisplayBatch.mockReset();
  });

  afterEach(() => {
    workspaceDataStore.reset();
    vi.useRealTimers();
  });

  it('initializes global context without enumerating the user roster', async () => {
    await workspaceDataStore.initializeGlobal();

    expect(workspaceDataStore.workspaceId).toBe('global');
    expect(workspaceDataStore.initialized).toBe(true);
    expect(workspaceDataStore.users).toEqual([]);
    expect(mocks.getUsers).not.toHaveBeenCalled();
    expect(mocks.getUserDisplayBatch).not.toHaveBeenCalled();
  });

  it('loads every global catalog from one aggregate request', async () => {
    mocks.getGlobalBootstrap.mockResolvedValue({
      item_types: [{ id: 1, name: 'Task' }],
      statuses: [{ id: 2, name: 'Open' }],
      status_categories: [{ id: 3, name: 'To do' }],
      priorities: [{ id: 4, name: 'High' }],
      custom_field_definitions: [{ id: 5, name: 'Impact' }],
      custom_fields_error: null,
    });

    await workspaceDataStore.initializeGlobal();

    expect(mocks.getGlobalBootstrap).toHaveBeenCalledTimes(1);
    expect(workspaceDataStore.itemTypes).toEqual([{ id: 1, name: 'Task' }]);
    expect(workspaceDataStore.statuses).toEqual([{ id: 2, name: 'Open' }]);
    expect(workspaceDataStore.statusCategories).toEqual([{ id: 3, name: 'To do' }]);
    expect(workspaceDataStore.priorities).toEqual([{ id: 4, name: 'High' }]);
    expect(workspaceDataStore.customFieldDefinitions).toEqual([{ id: 5, name: 'Impact' }]);
    expect(mocks.getItemTypes).not.toHaveBeenCalled();
    expect(mocks.getStatuses).not.toHaveBeenCalled();
    expect(mocks.getStatusCategories).not.toHaveBeenCalled();
    expect(mocks.getPriorities).not.toHaveBeenCalled();
    expect(mocks.getCustomFields).not.toHaveBeenCalled();
  });

  it('warns and keeps custom fields empty when the aggregate omits them', async () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
    mocks.getGlobalBootstrap.mockResolvedValue({
      item_types: [],
      statuses: [],
      status_categories: [],
      priorities: [],
      custom_field_definitions: null,
      custom_fields_error: 'insufficient_permission',
    });

    await workspaceDataStore.initializeGlobal();

    expect(workspaceDataStore.customFieldDefinitions).toEqual([]);
    expect(warnSpy).toHaveBeenCalledWith(
      'WorkspaceDataStore: failed to load custom field definitions',
      'insufficient_permission'
    );
  });

  it('keeps hydrated users across a global refresh', async () => {
    mocks.getUserDisplayBatch.mockResolvedValue([{ id: 7, username: 'seven' }]);
    await workspaceDataStore.initializeGlobal();
    await workspaceDataStore.hydrateUsers([7]);
    expect(workspaceDataStore.users).toEqual([{ id: 7, username: 'seven' }]);

    await workspaceDataStore.refresh();

    expect(mocks.getGlobalBootstrap).toHaveBeenCalledTimes(2);
    expect(workspaceDataStore.users).toEqual([{ id: 7, username: 'seven' }]);
  });

  it('discards a global aggregate that resolves after the scope changes', async () => {
    let resolveBootstrap;
    mocks.getGlobalBootstrap.mockReturnValue(
      new Promise((resolve) => {
        resolveBootstrap = resolve;
      })
    );

    const initialization = workspaceDataStore.initializeGlobal();
    await Promise.resolve();
    workspaceDataStore.reset();
    resolveBootstrap({
      item_types: [{ id: 1, name: 'Task' }],
      statuses: [],
      status_categories: [],
      priorities: [],
      custom_field_definitions: [],
    });
    await initialization;

    expect(workspaceDataStore.workspaceId).toBeNull();
    expect(workspaceDataStore.initialized).toBe(false);
    expect(workspaceDataStore.itemTypes).toEqual([]);
  });

  it('hydrates only the requested user ids in one batch', async () => {
    workspaceDataStore.workspaceId = 'global';
    mocks.getUserDisplayBatch.mockImplementation(async (ids) =>
      ids.map((id) => ({ id, username: `user-${id}` }))
    );

    await workspaceDataStore.hydrateUsers([7, 7, 9]);

    expect(mocks.getUserDisplayBatch).toHaveBeenCalledTimes(1);
    expect(mocks.getUserDisplayBatch.mock.calls[0][0]).toEqual([7, 9]);
    expect(
      workspaceDataStore.users.map((user) => user.id).sort((a, b) => a - b)
    ).toEqual([7, 9]);
  });

  it('coalesces unknown ids from the same microtask into one batch', async () => {
    workspaceDataStore.workspaceId = 'global';
    mocks.getUserDisplayBatch.mockImplementation(async (ids) =>
      ids.map((id) => ({ id, username: `user-${id}` }))
    );

    await Promise.all([
      workspaceDataStore.hydrateUsers([5]),
      workspaceDataStore.hydrateUsers([6, 5]),
    ]);

    expect(mocks.getUserDisplayBatch).toHaveBeenCalledTimes(1);
    expect(mocks.getUserDisplayBatch.mock.calls[0][0]).toEqual([5, 6]);
  });

  it('splits more than 200 ids into bounded chunks', async () => {
    workspaceDataStore.workspaceId = 'global';
    mocks.getUserDisplayBatch.mockImplementation(async (ids) =>
      ids.map((id) => ({ id, username: `user-${id}` }))
    );
    const ids = Array.from({ length: 401 }, (_, index) => index + 1);

    await workspaceDataStore.hydrateUsers(ids);

    expect(mocks.getUserDisplayBatch).toHaveBeenCalledTimes(3);
    expect(mocks.getUserDisplayBatch.mock.calls.map(([chunk]) => chunk.length)).toEqual([
      200, 200, 1,
    ]);
  });

  it('negatively caches successful omissions for five minutes', async () => {
    vi.useFakeTimers();
    workspaceDataStore.workspaceId = 'global';
    mocks.getUserDisplayBatch.mockResolvedValue([]);

    await workspaceDataStore.hydrateUsers([5]);
    await workspaceDataStore.hydrateUsers([5]);
    expect(mocks.getUserDisplayBatch).toHaveBeenCalledTimes(1);

    vi.advanceTimersByTime(5 * 60 * 1000 + 1);
    await workspaceDataStore.hydrateUsers([5]);
    expect(mocks.getUserDisplayBatch).toHaveBeenCalledTimes(2);
  });

  it('does not negatively cache transient failures', async () => {
    workspaceDataStore.workspaceId = 'global';
    mocks.getUserDisplayBatch
      .mockRejectedValueOnce(Object.assign(new Error('offline'), { status: 0 }))
      .mockResolvedValueOnce([{ id: 5, username: 'five' }]);

    await workspaceDataStore.hydrateUsers([5]);
    expect(workspaceDataStore.users).toEqual([]);

    await workspaceDataStore.hydrateUsers([5]);
    expect(mocks.getUserDisplayBatch).toHaveBeenCalledTimes(2);
    expect(workspaceDataStore.users).toEqual([{ id: 5, username: 'five' }]);
  });

  it('reuses fresh positive cache entries until five minutes elapse', async () => {
    vi.useFakeTimers();
    workspaceDataStore.workspaceId = 'global';
    mocks.getUserDisplayBatch.mockResolvedValue([{ id: 5, username: 'five' }]);

    await workspaceDataStore.hydrateUsers([5]);
    await workspaceDataStore.hydrateUsers([5]);
    expect(mocks.getUserDisplayBatch).toHaveBeenCalledTimes(1);

    vi.advanceTimersByTime(5 * 60 * 1000 + 1);
    await workspaceDataStore.hydrateUsers([5]);
    expect(mocks.getUserDisplayBatch).toHaveBeenCalledTimes(2);
  });

  it('discards hydration results after the scope changes', async () => {
    workspaceDataStore.workspaceId = 'global';
    let resolveBatch;
    mocks.getUserDisplayBatch.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveBatch = resolve;
        })
    );

    const hydration = workspaceDataStore.hydrateUsers([5]);
    await Promise.resolve();
    workspaceDataStore.reset();
    resolveBatch([{ id: 5, username: 'five' }]);
    await hydration;

    expect(workspaceDataStore.users).toEqual([]);
  });
});
