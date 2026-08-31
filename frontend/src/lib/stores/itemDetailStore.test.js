import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api.js', () => ({
  api: {
    items: {
      get: vi.fn(),
      getByKey: vi.fn(),
      getDetailSummary: vi.fn(),
      getDetailSummaryByKey: vi.fn(),
      getChildren: vi.fn(),
      getAncestors: vi.fn(),
      getAll: vi.fn(),
      getAvailableStatusTransitions: vi.fn(),
      getWatchStatus: vi.fn(),
      update: vi.fn(),
      transition: vi.fn(),
    },
    links: {
      create: vi.fn(),
      delete: vi.fn(),
      getForItem: vi.fn(),
    },
    time: {
      projects: {
        getByWorkspace: vi.fn(),
      },
      worklogs: {
        getByItem: vi.fn(),
      },
    },
    customerOrganisations: {
      getAll: vi.fn(),
    },
    workspaces: {
      get: vi.fn(),
      getAll: vi.fn(),
      getBootstrap: vi.fn(),
    },
    linkTypes: {
      getAll: vi.fn(),
    },
    customFields: {
      getAll: vi.fn(),
    },
    milestones: {
      getAll: vi.fn(),
    },
    iterations: {
      getAll: vi.fn(),
    },
    requestTypes: {
      getFields: vi.fn(),
    },
    configurationSets: {
      get: vi.fn(),
    },
    priorities: {
      getAll: vi.fn(),
    },
    itemTypes: {
      getAll: vi.fn(),
    },
    hierarchyLevels: {
      getAll: vi.fn(),
    },
    screens: {
      get: vi.fn(),
    },
    getDiagrams: vi.fn(),
    actions: {
      getAll: vi.fn(),
    },
  },
}));

const { api } = await import('../api.js');
const { itemDetailStore } = await import('./itemDetailStore.svelte.js');
const { workspaceDataStore } = await import('./workspaceDataStore.svelte.js');

function mockSuccessfulRelatedLoads() {
  api.workspaces.get.mockResolvedValue({ id: 1, configuration_set_id: null });
  api.workspaces.getBootstrap.mockImplementation(async (id) => ({
    workspace: { id, configuration_set_id: null },
    homepage_layout: { sections: [], widgets: [] },
    statuses: [],
    status_categories: [],
    users: [],
    milestones: [],
    iterations: [],
    projects: [],
    item_types: [],
    priorities: [],
    custom_field_definitions: [],
  }));
  api.linkTypes.getAll.mockResolvedValue([]);
  api.links.getForItem.mockResolvedValue({ outgoing: [], incoming: [] });
  api.customFields.getAll.mockResolvedValue({ data: [] });
  api.milestones.getAll.mockResolvedValue([]);
  api.iterations.getAll.mockResolvedValue([]);
  api.time.projects.getByWorkspace.mockResolvedValue([]);
  api.priorities.getAll.mockResolvedValue([]);
  api.items.getAvailableStatusTransitions.mockResolvedValue({});
  api.items.getWatchStatus.mockResolvedValue({});
  api.itemTypes.getAll.mockResolvedValue([]);
  api.hierarchyLevels.getAll.mockResolvedValue([]);
  api.items.getChildren.mockResolvedValue([]);
  api.screens.get.mockResolvedValue({ tabs: [] });
  api.getDiagrams.mockResolvedValue([]);
  api.actions.getAll.mockResolvedValue([]);
}

function detailSummary(item, overrides = {}) {
  return {
    item,
    links: { outgoing: [], incoming: [] },
    link_types: [],
    request_type_fields: [],
    transitions: { available_transitions: [], pending_approval: null },
    watching: false,
    children: [],
    ancestors: [],
    current_item_type: null,
    current_hierarchy_level: null,
    available_sub_issue_types: [],
    priorities: [],
    screen_context: { edit: null, view: null },
    manual_actions: [],
    ...overrides,
  };
}

describe('itemDetailStore.loadItem request graph', () => {
  beforeEach(() => {
    itemDetailStore.reset();
    workspaceDataStore.reset();
    vi.clearAllMocks();
    mockSuccessfulRelatedLoads();
  });

  it('loads a key-addressed item and its above-the-fold context in one summary request', async () => {
    const resolvedItem = {
      id: 42,
      workspace_id: 1,
      title: 'Resolved by key',
      parent_id: null,
      item_type_id: null,
      request_type_id: null,
    };
    api.items.getDetailSummaryByKey.mockResolvedValue(detailSummary(resolvedItem));

    await itemDetailStore.loadItem('WS', 7, { workspaceKey: 'WS', itemNumber: 7 });

    expect(api.items.getDetailSummaryByKey).toHaveBeenCalledWith(
      'WS',
      7,
      expect.objectContaining({ signal: expect.any(AbortSignal) })
    );
    expect(api.items.getByKey).not.toHaveBeenCalled();
    expect(api.items.get).not.toHaveBeenCalled();
    expect(api.getDiagrams).not.toHaveBeenCalled();
    expect(itemDetailStore.diagramsLoaded).toBe(false);
    expect(itemDetailStore.item).toMatchObject(resolvedItem);
    expect(itemDetailStore.itemId).toBe(42);
  });

  it('aborts a superseded item load and keeps only the latest result', async () => {
    let firstSignal;
    api.items.getDetailSummary.mockImplementation((id, { signal }) => {
      if (id === 101) {
        firstSignal = signal;
        return new Promise((_resolve, reject) => {
          signal.addEventListener(
            'abort',
            () => reject(Object.assign(new Error('aborted'), { name: 'AbortError' })),
            { once: true }
          );
        });
      }
      return Promise.resolve(
        detailSummary({
          id,
          workspace_id: 1,
          title: 'Latest item',
          parent_id: null,
          item_type_id: null,
          request_type_id: null,
        })
      );
    });

    const firstLoad = itemDetailStore.loadItem(1, 101);
    const latestLoad = itemDetailStore.loadItem(1, 102);
    await Promise.all([firstLoad, latestLoad]);

    expect(firstSignal.aborted).toBe(true);
    expect(itemDetailStore.item.id).toBe(102);
    expect(itemDetailStore.itemId).toBe(102);
    expect(itemDetailStore.error).toBeNull();
  });

  it('reuses initialized workspace references and keeps diagrams deferred', async () => {
    workspaceDataStore.workspaceId = 1;
    workspaceDataStore.workspace = { id: 1, configuration_set_id: null };
    workspaceDataStore.customFieldDefinitions = [{ id: 7, name: 'Shared field' }];
    workspaceDataStore.milestones = [{ id: 8, name: 'Shared milestone' }];
    workspaceDataStore.iterations = [{ id: 10, name: 'Shared iteration' }];
    workspaceDataStore.priorities = [{ id: 11, name: 'Shared priority', sort_order: 0 }];
    workspaceDataStore.projects = [{ id: 12, name: 'Shared project' }];
    workspaceDataStore.itemTypes = [{ id: 9, name: 'Shared type', hierarchy_level: 0 }];
    workspaceDataStore.initialized = true;
    api.items.getDetailSummary.mockResolvedValue(
      detailSummary(
        {
          id: 42,
          workspace_id: 1,
          title: 'Shared references',
          parent_id: null,
          item_type_id: 9,
          request_type_id: null,
        },
        {
          current_item_type: { id: 9, name: 'Shared type', hierarchy_level: 0 },
          current_hierarchy_level: { level: 0 },
        }
      )
    );

    await itemDetailStore.loadItem(1, 42);

    expect(api.workspaces.get).not.toHaveBeenCalled();
    expect(api.customFields.getAll).not.toHaveBeenCalled();
    expect(api.milestones.getAll).not.toHaveBeenCalled();
    expect(api.iterations.getAll).not.toHaveBeenCalled();
    expect(api.priorities.getAll).not.toHaveBeenCalled();
    expect(api.itemTypes.getAll).not.toHaveBeenCalled();
    expect(api.time.projects.getByWorkspace).not.toHaveBeenCalled();
    expect(api.items.getAvailableStatusTransitions).not.toHaveBeenCalled();
    expect(api.items.getWatchStatus).not.toHaveBeenCalled();
    expect(api.items.getChildren).not.toHaveBeenCalled();
    expect(api.hierarchyLevels.getAll).not.toHaveBeenCalled();
    expect(api.screens.get).not.toHaveBeenCalled();
    expect(api.actions.getAll).not.toHaveBeenCalled();
    expect(api.getDiagrams).not.toHaveBeenCalled();
    expect(itemDetailStore.diagramsLoaded).toBe(false);
    expect(itemDetailStore.customFieldDefinitions).toEqual([{ id: 7, name: 'Shared field' }]);
    expect(itemDetailStore.milestones).toEqual([{ id: 8, name: 'Shared milestone' }]);
    expect(itemDetailStore.iterations).toEqual([{ id: 10, name: 'Shared iteration' }]);
    expect(itemDetailStore.priorities).toEqual([
      { id: 11, name: 'Shared priority', sort_order: 0 },
    ]);
    expect(itemDetailStore.timeProjects).toEqual([{ id: 12, name: 'Shared project' }]);
    expect(itemDetailStore.currentItemType).toMatchObject({ id: 9, name: 'Shared type' });
  });
});

describe('itemDetailStore.loadChildItems', () => {
  beforeEach(() => {
    itemDetailStore.reset();
    itemDetailStore.itemId = 42;
    itemDetailStore.item = { id: 42 };
    itemDetailStore.childItems = [];
    itemDetailStore.loadingChildItems = false;
    api.items.getChildren.mockReset();
  });

  it('keeps the existing child item array when fetched summary data is unchanged', async () => {
    const currentChildren = [
      {
        id: 7,
        workspace_id: 2,
        workspace_key: 'WI',
        workspace_item_number: 101,
        item_type_id: 5,
        title: 'Child item',
        status_id: 1,
        status_name: 'Open',
        status_color: '#94a3b8',
        frac_index: 'a0',
        description: 'local expanded data should not matter',
      },
    ];
    itemDetailStore.childItems = currentChildren;
    const currentRef = itemDetailStore.childItems;
    api.items.getChildren.mockResolvedValue([
      {
        id: 7,
        workspace_id: 2,
        workspace_key: 'WI',
        workspace_item_number: 101,
        item_type_id: 5,
        title: 'Child item',
        status_id: 1,
        status_name: 'Open',
        status_color: '#94a3b8',
        frac_index: 'a0',
      },
    ]);

    await itemDetailStore.loadChildItems();

    expect(itemDetailStore.childItems).toBe(currentRef);
  });

  it('replaces child items when display-relevant data changes', async () => {
    const currentChildren = [{ id: 7, title: 'Old title' }];
    const nextChildren = [{ id: 7, title: 'New title' }];
    itemDetailStore.childItems = currentChildren;
    const currentRef = itemDetailStore.childItems;
    api.items.getChildren.mockResolvedValue({ items: nextChildren });

    await itemDetailStore.loadChildItems();

    expect(itemDetailStore.childItems).not.toBe(currentRef);
    expect(itemDetailStore.childItems).toEqual(nextChildren);
  });
});

describe('itemDetailStore.refreshCurrentItem', () => {
  beforeEach(() => {
    itemDetailStore.reset();
    itemDetailStore.itemId = 42;
    itemDetailStore.item = {
      id: 42,
      status_id: 1,
      item_type_id: 2,
      parent_id: null,
      custom_field_values: { 7: 'stored value', 8: 'old server value' },
    };
    itemDetailStore.loading = false;
    api.items.get.mockReset();
  });

  it('keeps an active custom-field draft while refreshing inactive values', async () => {
    itemDetailStore.startEditing('custom_field_7');
    itemDetailStore.editing.customFields.values[7] = 'local draft';
    api.items.get.mockResolvedValue({
      id: 42,
      status_id: 1,
      item_type_id: 2,
      parent_id: null,
      custom_field_values: { 7: 'new server value', 8: 'fresh server value' },
    });

    await itemDetailStore.refreshCurrentItem();

    expect(api.items.get).toHaveBeenCalledWith(
      42,
      expect.objectContaining({ cache: 'no-store', signal: expect.any(AbortSignal) })
    );
    expect(itemDetailStore.editing.customFields.values).toEqual({
      7: 'local draft',
      8: 'fresh server value',
    });
    expect(itemDetailStore.editing.customFields.active[7]).toBe(true);
  });
});

describe('itemDetailStore.saveField', () => {
  beforeEach(() => {
    itemDetailStore.reset();
    itemDetailStore.item = { id: 42, estimate_minutes: null, story_points: null };
    itemDetailStore.saving = false;
    itemDetailStore.hasChanges = false;
    api.items.update.mockReset();
    api.items.update.mockImplementation(async (_id, data) => ({ ...data }));
    api.items.transition.mockReset();
    api.items.getAvailableStatusTransitions.mockReset();
  });

  it('persists estimate_minutes updates', async () => {
    await itemDetailStore.saveField('estimate_minutes', 240);

    expect(api.items.update).toHaveBeenCalledWith(42, { estimate_minutes: 240 });
    expect(itemDetailStore.item.estimate_minutes).toBe(240);
    expect(itemDetailStore.hasChanges).toBe(true);
  });

  it('persists estimate clears', async () => {
    itemDetailStore.item = { id: 42, estimate_minutes: 240 };

    await itemDetailStore.saveField('estimate_minutes', null);

    expect(api.items.update).toHaveBeenCalledWith(42, { estimate_minutes: null });
    expect(itemDetailStore.item.estimate_minutes).toBeNull();
  });

  it('refreshes available transitions after changing status', async () => {
    const nextTransitions = [{ id: 3, name: 'Done' }];
    itemDetailStore.item = { id: 42, status_id: 1 };
    itemDetailStore.availableStatusTransitions = [{ id: 2, name: 'In Progress' }];
    api.items.transition.mockResolvedValue({ id: 42, status_id: 2 });
    api.items.getAvailableStatusTransitions.mockResolvedValue({
      available_transitions: nextTransitions,
    });

    await itemDetailStore.saveField('status_id', 2);

    expect(api.items.transition).toHaveBeenCalledWith(42, 2);
    expect(api.items.getAvailableStatusTransitions).toHaveBeenCalledWith(42, {});
    expect(itemDetailStore.item.status_id).toBe(2);
    expect(itemDetailStore.availableStatusTransitions).toEqual(nextTransitions);
  });
});

describe('itemDetailStore optional item data', () => {
  beforeEach(() => {
    itemDetailStore.reset();
    itemDetailStore.itemId = 42;
    itemDetailStore.item = { id: 42 };
    api.items.getAll.mockReset();
    api.links.getForItem.mockReset();
    api.time.worklogs.getByItem.mockReset();
    api.customerOrganisations.getAll.mockReset();
    api.workspaces.getAll.mockReset();
    api.getDiagrams.mockReset();
  });

  it('single-flights worklogs for the open item', async () => {
    let resolveWorklogs;
    api.time.worklogs.getByItem.mockReturnValue(
      new Promise((resolve) => {
        resolveWorklogs = resolve;
      })
    );

    const first = itemDetailStore.loadWorklogs();
    const second = itemDetailStore.loadWorklogs();
    expect(api.time.worklogs.getByItem).toHaveBeenCalledTimes(1);

    resolveWorklogs([{ id: 9, duration_minutes: 30 }]);
    await Promise.all([first, second]);

    expect(itemDetailStore.timeWorklogs).toEqual([{ id: 9, duration_minutes: 30 }]);
  });

  it('loads time-modal-only picker data once', async () => {
    api.customerOrganisations.getAll.mockResolvedValue([{ id: 1 }]);
    api.items.getAll.mockResolvedValue({ items: [{ id: 2 }] });
    api.workspaces.getAll.mockResolvedValue([{ id: 3 }]);

    await Promise.all([itemDetailStore.loadTimeModalData(), itemDetailStore.loadTimeModalData()]);
    await itemDetailStore.loadTimeModalData();

    expect(api.customerOrganisations.getAll).toHaveBeenCalledTimes(1);
    expect(api.items.getAll).toHaveBeenCalledTimes(1);
    expect(api.workspaces.getAll).toHaveBeenCalledTimes(1);
    expect(itemDetailStore.customers).toEqual([{ id: 1 }]);
    expect(itemDetailStore.workItems).toEqual([{ id: 2 }]);
    expect(itemDetailStore.workspaces).toEqual([{ id: 3 }]);
  });

  it('single-flights diagram requests and caches the result', async () => {
    let resolveDiagrams;
    api.getDiagrams.mockReturnValue(
      new Promise((resolve) => {
        resolveDiagrams = resolve;
      })
    );

    const first = itemDetailStore.loadDiagrams();
    const second = itemDetailStore.loadDiagrams();
    expect(api.getDiagrams).toHaveBeenCalledTimes(1);
    expect(itemDetailStore.diagramsLoaded).toBe(false);

    resolveDiagrams([{ id: 5, name: 'Architecture' }]);
    await Promise.all([first, second]);

    expect(itemDetailStore.diagramsLoaded).toBe(true);
    expect(itemDetailStore.diagrams).toEqual([{ id: 5, name: 'Architecture' }]);

    await itemDetailStore.loadDiagrams();
    expect(api.getDiagrams).toHaveBeenCalledTimes(1);
  });

  it('refreshes links without reloading the complete item', async () => {
    api.links.getForItem.mockResolvedValue({
      outgoing: [{ id: 1 }],
      incoming: [{ id: 2 }],
    });

    await itemDetailStore.loadLinks();

    expect(api.links.getForItem).toHaveBeenCalledWith(
      'items',
      42,
      expect.objectContaining({ signal: expect.any(AbortSignal) })
    );
    expect(itemDetailStore.itemLinks).toEqual([{ id: 1 }, { id: 2 }]);
  });
});
