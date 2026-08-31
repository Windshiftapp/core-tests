import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  getBootstrap: vi.fn(),
  getWorkspace: vi.fn(),
  getStatuses: vi.fn(),
  getProjects: vi.fn(),
  getAssignableUsers: vi.fn(),
  getItemTypes: vi.fn(),
  getStatusCategories: vi.fn(),
  getMilestones: vi.fn(),
  getIterations: vi.fn(),
  getPriorities: vi.fn(),
  getCustomFields: vi.fn(),
}));

vi.mock('../api.js', () => ({
  api: {
    workspaces: {
      getBootstrap: mocks.getBootstrap,
      get: mocks.getWorkspace,
      getStatuses: mocks.getStatuses,
      getProjects: mocks.getProjects,
    },
    itemTypes: { getAll: mocks.getItemTypes },
    statusCategories: { getAll: mocks.getStatusCategories },
    getAssignableUsers: mocks.getAssignableUsers,
    milestones: { getAll: mocks.getMilestones },
    iterations: { getAll: mocks.getIterations },
    priorities: { getAll: mocks.getPriorities },
    customFields: { getAll: mocks.getCustomFields },
  },
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
});
