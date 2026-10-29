import { beforeEach, describe, expect, it, vi } from 'vitest';
import { configurationSets } from '../api/configuration.js';
import { items } from '../api/items.js';
import { customFields, hierarchyLevels, itemTypes, linkTypes, links, priorities, screens } from '../api/configuration.js';
import { time } from '../api/time.js';
import { customerOrganisations } from '../api/portal.js';
import { workspaces } from '../api/workspaces.js';
import { iterations, milestones } from '../api/milestones.js';
import { requestTypes } from '../api/channels.js';
import { getDiagrams } from '../api/misc.js';
import { actions } from '../api/actions.js';









vi.mock('../api/items.js', () => ({
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
}))

vi.mock('../api/configuration.js', () => ({
  links: {
        create: vi.fn(),
        delete: vi.fn(),
        getForItem: vi.fn(),
      },
  linkTypes: {
        getAll: vi.fn(),
      },
  customFields: {
        getAll: vi.fn(),
      },
  configurationSets: {
        get: vi.fn(),
        getAll: vi.fn(),
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
}))

vi.mock('../api/time.js', () => ({
  time: {
        projects: {
          getByWorkspace: vi.fn(),
        },
        worklogs: {
          getByItem: vi.fn(),
        },
      },
}))

vi.mock('../api/portal.js', () => ({
  customerOrganisations: {
        getAll: vi.fn(),
      },
}))

vi.mock('../api/workspaces.js', () => ({
  workspaces: {
        get: vi.fn(),
        getAll: vi.fn(),
        getPage: vi.fn(),
        getBootstrap: vi.fn(),
        getEffectiveConfig: vi.fn(),
      },
}))

vi.mock('../api/milestones.js', () => ({
  milestones: {
        getAll: vi.fn(),
      },
  iterations: {
        getAll: vi.fn(),
      },
  milestoneCategories: { getAll: vi.fn().mockResolvedValue([]) },
}))

vi.mock('../api/channels.js', () => ({
  requestTypes: {
        getFields: vi.fn(),
      },
}))

vi.mock('../api/misc.js', () => ({
  getDiagrams: vi.fn(),
}))

vi.mock('../api/actions.js', () => ({
  actions: {
        getAll: vi.fn(),
      },
}));


const { itemDetailStore } = await import('./itemDetailStore.svelte.js');
const { workspaceDataStore } = await import('./workspaceDataStore.svelte.js');

function mockSuccessfulRelatedLoads() {
  workspaces.get.mockResolvedValue({ id: 1, configuration_set_id: null });
  workspaces.getBootstrap.mockImplementation(async (id) => ({
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
  linkTypes.getAll.mockResolvedValue([]);
  links.getForItem.mockResolvedValue({ outgoing: [], incoming: [] });
  customFields.getAll.mockResolvedValue([]);
  milestones.getAll.mockResolvedValue([]);
  iterations.getAll.mockResolvedValue([]);
  time.projects.getByWorkspace.mockResolvedValue([]);
  priorities.getAll.mockResolvedValue([]);
  items.getAvailableStatusTransitions.mockResolvedValue({});
  items.getWatchStatus.mockResolvedValue({});
  itemTypes.getAll.mockResolvedValue([]);
  hierarchyLevels.getAll.mockResolvedValue([]);
  items.getChildren.mockResolvedValue([]);
  screens.get.mockResolvedValue({ tabs: [] });
  getDiagrams.mockResolvedValue([]);
  actions.getAll.mockResolvedValue([]);
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
    items.getDetailSummaryByKey.mockResolvedValue(detailSummary(resolvedItem));

    await itemDetailStore.loadItem('WS', 7, { workspaceKey: 'WS', itemNumber: 7 });

    expect(items.getDetailSummaryByKey).toHaveBeenCalledWith(
      'WS',
      7,
      expect.objectContaining({ signal: expect.any(AbortSignal) })
    );
    expect(items.getByKey).not.toHaveBeenCalled();
    expect(items.get).not.toHaveBeenCalled();
    expect(getDiagrams).not.toHaveBeenCalled();
    expect(itemDetailStore.diagramsLoaded).toBe(false);
    expect(itemDetailStore.item).toMatchObject(resolvedItem);
    expect(itemDetailStore.itemId).toBe(42);
  });

  it('leaves no screen fields from the previous item while the next item loads', async () => {
    console.log('t3 start cache', JSON.stringify(Object.keys(workspaceDataStore.screenConfigs)), 'wsId', String(workspaceDataStore.workspaceId));
    items.getDetailSummary.mockResolvedValue(
      detailSummary(
        {
          id: 42,
          workspace_id: 1,
          title: 'First item',
          parent_id: null,
          item_type_id: 9,
          request_type_id: null,
        },
        {
          screen_context: {
            edit: { id: 31, fields: [{ field_type: 'system', field_identifier: 'estimate' }] },
            view: null,
          },
        }
      )
    );
    await itemDetailStore.loadItem(1, 42);
    console.log('t3 after load1 cache', JSON.stringify(Object.keys(workspaceDataStore.screenConfigs)), 'cfgcalls', workspaces.getEffectiveConfig.mock.calls.length);
    expect(itemDetailStore.workspaceScreenSystemFields).toContain('estimate');

    // The next load's own hydration fetch stays pending so we can observe the
    // cleared state between the item assignment and hydration completing.
    let releaseHydration;
    workspaces.getEffectiveConfig.mockReturnValue(
      new Promise((resolve) => (releaseHydration = resolve))
    );
    items.getDetailSummary.mockResolvedValue(
      detailSummary({
        id: 43,
        workspace_id: 1,
        title: 'Second item',
        parent_id: null,
        item_type_id: 9,
        request_type_id: null,
      })
    );

    const nextLoad = itemDetailStore.loadItem(1, 43);
    await vi.waitFor(() => expect(itemDetailStore.item.id).toBe(43));
    expect(itemDetailStore.workspaceScreenFields).toEqual([]);
    expect(itemDetailStore.workspaceScreenSystemFields).toEqual([]);

    releaseHydration({
      config_set_id: 5,
      screens: { create: 31, edit: 31, view: 31 },
      item_type_ids: [9],
      default_item_type_id: null,
      edit_screen: { id: 31, fields: [{ field_type: 'system', field_identifier: 'priority' }] },
      view_screen: null,
      priorities: [],
    });
    await nextLoad;
    expect(itemDetailStore.workspaceScreenSystemFields).toEqual(
      expect.arrayContaining(['priority', 'status'])
    );
  });

  it('preserves active custom-field drafts and screen fields across a same-item reload', async () => {
    const screenContext = {
      edit: { id: 31, fields: [{ field_type: 'system', field_identifier: 'estimate' }] },
      view: null,
    };
    items.getDetailSummary.mockResolvedValue(
      detailSummary(
        {
          id: 42,
          workspace_id: 1,
          title: 'Reconciled item',
          parent_id: null,
          item_type_id: 9,
          request_type_id: null,
          custom_field_values: { 7: 'stored value', 8: 'old server value' },
        },
        { screen_context: screenContext }
      )
    );
    await itemDetailStore.loadItem(1, 42);
    expect(itemDetailStore.workspaceScreenSystemFields).toContain('estimate');

    itemDetailStore.startEditing('custom_field_7');
    itemDetailStore.editing.customFields.values[7] = 'local draft';

    // A background reconcile of the same item ships a fresh snapshot while the
    // user is mid-edit; the draft and the resolved screen fields must survive.
    items.getDetailSummary.mockResolvedValue(
      detailSummary(
        {
          id: 42,
          workspace_id: 1,
          title: 'Reconciled item',
          parent_id: null,
          item_type_id: 9,
          request_type_id: null,
          custom_field_values: { 7: 'new server value', 8: 'fresh server value' },
        },
        { screen_context: screenContext }
      )
    );
    await itemDetailStore.loadItem(1, 42);

    expect(itemDetailStore.editing.customFields.active[7]).toBe(true);
    expect(itemDetailStore.editing.customFields.values).toEqual({
      7: 'local draft',
      8: 'fresh server value',
    });
    expect(itemDetailStore.workspaceScreenSystemFields).toContain('estimate');
  });

  it('hydrates screen fields via the effective-config cache when the summary has none (GH #261)', async () => {
    // Regression for GH #261: per-item-type screens must resolve correctly on
    // the detail page. When the summary's configuration section is missing,
    // the store hydrates through the workspace's shared effective-config
    // cache instead of re-implementing resolution client-side.
    workspaces.getEffectiveConfig.mockResolvedValue({
      config_set_id: 5,
      screens: { create: 31, edit: 31, view: 33 },
      item_type_ids: [9],
      default_item_type_id: null,
      edit_screen: { id: 31, fields: [{ field_type: 'system', field_identifier: 'priority' }] },
      view_screen: { id: 33, fields: [{ field_type: 'system', field_identifier: 'estimate' }] },
      priorities: [],
    });
    items.getDetailSummary.mockResolvedValue(
      detailSummary({
        id: 42,
        workspace_id: 1,
        title: 'Default config set item',
        parent_id: null,
        item_type_id: 9,
        request_type_id: null,
      })
    );

    await itemDetailStore.loadItem(1, 42);

    expect(workspaces.getEffectiveConfig).toHaveBeenCalledWith(
      1,
      9,
      expect.objectContaining({ readPolicy: { priority: 'foreground' } })
    );
    // The edit and view screens differ, so the visible system fields are the
    // union of both.
    expect(itemDetailStore.workspaceScreenSystemFields).toEqual(
      expect.arrayContaining(['estimate', 'priority', 'status'])
    );
  });



  it('aborts a superseded item load and keeps only the latest result', async () => {
    let firstSignal;
    items.getDetailSummary.mockImplementation((id, { signal }) => {
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
    const [firstResult, latestResult] = await Promise.all([firstLoad, latestLoad]);

    expect(firstSignal.aborted).toBe(true);
    // The superseded caller must be able to tell that its request did not win,
    // instead of reading the store's current (102) item as if it were its own.
    expect(firstResult).toEqual({ status: 'superseded' });
    expect(latestResult).toMatchObject({ status: 'success' });
    expect(latestResult.item.id).toBe(102);
    expect(itemDetailStore.item.id).toBe(102);
    expect(itemDetailStore.itemId).toBe(102);
    expect(itemDetailStore.error).toBeNull();
  });

  it('reports a failed summary request as an error result and clears the item', async () => {
    items.getDetailSummary.mockRejectedValue(new Error('summary unavailable'));

    const result = await itemDetailStore.loadItem(1, 42);

    expect(result).toEqual({ status: 'error', error: 'summary unavailable' });
    expect(itemDetailStore.item).toBeNull();
    expect(itemDetailStore.error).toBe('summary unavailable');
  });

  it('reports a superseded load when a newer load starts during screen-field hydration', async () => {
    // Hold the first load inside its screen-field hydration so the second load
    // can start after the item assignment but before the first load returns.
    let releaseHydration;
    workspaces.getEffectiveConfig.mockReturnValue(
      new Promise((resolve) => (releaseHydration = resolve))
    );
    items.getDetailSummary.mockImplementation((id) =>
      Promise.resolve(
        detailSummary({
          id,
          workspace_id: 1,
          title: `Item ${id}`,
          parent_id: null,
          item_type_id: 9,
          request_type_id: null,
        })
      )
    );

    const firstLoad = itemDetailStore.loadItem(1, 42);
    await vi.waitFor(() => expect(itemDetailStore.item?.id).toBe(42));

    const secondLoad = itemDetailStore.loadItem(1, 43);
    await vi.waitFor(() => expect(itemDetailStore.item?.id).toBe(43));

    releaseHydration({
      config_set_id: 5,
      screens: { create: 31, edit: 31, view: 31 },
      item_type_ids: [9],
      default_item_type_id: null,
      edit_screen: { id: 31, fields: [] },
      view_screen: null,
      priorities: [],
    });

    const [firstResult, secondResult] = await Promise.all([firstLoad, secondLoad]);

    expect(firstResult).toEqual({ status: 'superseded' });
    expect(secondResult).toMatchObject({ status: 'success' });
    expect(secondResult.item.id).toBe(43);
  });

  it('hydrates workspace references from one bootstrap when the shared store serves another scope', async () => {
    // Regression: a global board owns the shared store while the item modal is
    // open. The detail must hydrate the item's own workspace from one
    // aggregate bootstrap instead of six independent domain reads, and must
    // leave the global scope active.
    workspaceDataStore.workspaceId = 'global';
    workspaceDataStore.initialized = true;
    workspaces.getBootstrap.mockResolvedValue({
      workspace: { id: 1, configuration_set_id: null, milestone_categories: [3] },
      custom_field_definitions: [{ id: 7, name: 'Direct field' }],
      milestones: [
        { id: 8, name: 'Direct milestone', category_id: 3 },
        { id: 18, name: 'Other milestone', category_id: 4 },
      ],
      iterations: [{ id: 10, name: 'Direct iteration' }],
      projects: [{ id: 12, name: 'Direct project' }],
      item_types: [{ id: 9, name: 'Direct type', hierarchy_level: 0 }],
      priorities: [{ id: 11, name: 'Direct priority', sort_order: 0 }],
    });
    items.getDetailSummary.mockResolvedValue(
      detailSummary({
        id: 42,
        workspace_id: 1,
        title: 'Direct references',
        parent_id: null,
        item_type_id: 9,
        request_type_id: null,
      })
    );

    const result = await itemDetailStore.loadItem(1, 42);

    expect(result).toMatchObject({ status: 'success' });
    expect(workspaces.getBootstrap).toHaveBeenCalledWith(
      1,
      expect.objectContaining({ readPolicy: { priority: 'foreground' } })
    );
    // The six independent loaders must not run.
    expect(workspaces.get).not.toHaveBeenCalled();
    expect(customFields.getAll).not.toHaveBeenCalled();
    expect(milestones.getAll).not.toHaveBeenCalled();
    expect(iterations.getAll).not.toHaveBeenCalled();
    expect(time.projects.getByWorkspace).not.toHaveBeenCalled();
    expect(itemTypes.getAll).not.toHaveBeenCalled();

    expect(itemDetailStore.customFieldDefinitions).toEqual([{ id: 7, name: 'Direct field' }]);
    expect(itemDetailStore.milestones).toEqual([
      { id: 8, name: 'Direct milestone', category_id: 3 },
    ]);
    expect(itemDetailStore.iterations).toEqual([{ id: 10, name: 'Direct iteration' }]);
    expect(itemDetailStore.timeProjects).toEqual([{ id: 12, name: 'Direct project' }]);
    expect(itemDetailStore.itemTypes).toEqual([{ id: 9, name: 'Direct type', hierarchy_level: 0 }]);
    // The global scope stays active; the modal workspace is not activated.
    expect(workspaceDataStore.workspaceId).toBe('global');
  });

  it('does not borrow the active scope priorities when a foreign snapshot is unavailable', async () => {
    // Regression: a 404 bootstrap for a foreign workspace must not fall back to
    // the global scope's priorities in the item editor.
    workspaceDataStore.workspaceId = 'global';
    workspaceDataStore.initialized = true;
    workspaceDataStore.priorities = [{ id: 99, name: 'Global priority', sort_order: 0 }];
    workspaces.getBootstrap.mockRejectedValue(
      Object.assign(new Error('not found'), { status: 404 })
    );
    items.getDetailSummary.mockResolvedValue(
      detailSummary({
        id: 42,
        workspace_id: 1,
        title: 'Foreign item',
        parent_id: null,
        item_type_id: 9,
        request_type_id: null,
      })
    );

    const result = await itemDetailStore.loadItem(1, 42);

    expect(result).toMatchObject({ status: 'success' });
    expect(itemDetailStore.priorities).toEqual([]);
  });

  it('shares one bootstrap request across concurrent detail loads for the same workspace', async () => {
    workspaceDataStore.workspaceId = 'global';
    workspaceDataStore.initialized = true;
    let resolveBootstrap;
    workspaces.getBootstrap.mockReturnValue(
      new Promise((resolve) => {
        resolveBootstrap = resolve;
      })
    );
    items.getDetailSummary.mockResolvedValue(
      detailSummary({
        id: 42,
        workspace_id: 1,
        title: 'Shared snapshot',
        parent_id: null,
        item_type_id: 9,
        request_type_id: null,
      })
    );

    const first = itemDetailStore.loadItem(1, 42);
    const second = itemDetailStore.loadItem(1, 42);
    await vi.waitFor(() => expect(workspaces.getBootstrap).toHaveBeenCalledTimes(1));

    resolveBootstrap({
      workspace: { id: 1, configuration_set_id: null },
      custom_field_definitions: [],
      milestones: [],
      iterations: [],
      projects: [],
      item_types: [],
      priorities: [],
    });
    await Promise.allSettled([first, second]);
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
    items.getDetailSummary.mockResolvedValue(
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

    expect(workspaces.get).not.toHaveBeenCalled();
    expect(customFields.getAll).not.toHaveBeenCalled();
    expect(milestones.getAll).not.toHaveBeenCalled();
    expect(iterations.getAll).not.toHaveBeenCalled();
    expect(priorities.getAll).not.toHaveBeenCalled();
    expect(itemTypes.getAll).not.toHaveBeenCalled();
    expect(time.projects.getByWorkspace).not.toHaveBeenCalled();
    expect(items.getAvailableStatusTransitions).not.toHaveBeenCalled();
    expect(items.getWatchStatus).not.toHaveBeenCalled();
    expect(items.getChildren).not.toHaveBeenCalled();
    expect(hierarchyLevels.getAll).not.toHaveBeenCalled();
    expect(screens.get).not.toHaveBeenCalled();
    expect(actions.getAll).not.toHaveBeenCalled();
    expect(getDiagrams).not.toHaveBeenCalled();
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
    items.getChildren.mockReset();
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
    items.getChildren.mockResolvedValue([
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
    items.getChildren.mockResolvedValue({ items: nextChildren });

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
    items.get.mockReset();
  });

  it('keeps an active custom-field draft while refreshing inactive values', async () => {
    itemDetailStore.startEditing('custom_field_7');
    itemDetailStore.editing.customFields.values[7] = 'local draft';
    items.get.mockResolvedValue({
      id: 42,
      status_id: 1,
      item_type_id: 2,
      parent_id: null,
      custom_field_values: { 7: 'new server value', 8: 'fresh server value' },
    });

    await itemDetailStore.refreshCurrentItem();

    expect(items.get).toHaveBeenCalledWith(
      42,
      expect.objectContaining({ cache: 'no-store', signal: expect.any(AbortSignal) })
    );
    expect(itemDetailStore.editing.customFields.values).toEqual({
      7: 'local draft',
      8: 'fresh server value',
    });
    expect(itemDetailStore.editing.customFields.active[7]).toBe(true);
  });

  it('rejects a transient failure so the SSE layer can resume polling', async () => {
    const failure = Object.assign(new Error('offline'), { code: 'NETWORK_ERROR' });
    items.get.mockRejectedValue(failure);

    await expect(itemDetailStore.refreshCurrentItem()).rejects.toBe(failure);
  });

  it('queues a refresh requested while a refresh is in flight', async () => {
    let resolveFirst;
    let resolveSecond;
    items.get
      .mockImplementationOnce(
        () => new Promise((resolve) => {
          resolveFirst = resolve;
        })
      )
      .mockImplementationOnce(
        () => new Promise((resolve) => {
          resolveSecond = resolve;
        })
      );

    const firstCall = itemDetailStore.refreshCurrentItem();
    const secondCall = itemDetailStore.refreshCurrentItem();

    // The second request is queued rather than dropped or overlapped.
    expect(items.get).toHaveBeenCalledTimes(1);
    await secondCall;

    resolveFirst({ id: 42, status_id: 1, item_type_id: 2, parent_id: null });
    await firstCall;
    await vi.waitFor(() => expect(items.get).toHaveBeenCalledTimes(2));

    resolveSecond({ id: 42, status_id: 1, item_type_id: 2, parent_id: null });
  });
});

describe('itemDetailStore.saveField', () => {
  beforeEach(() => {
    itemDetailStore.reset();
    itemDetailStore.item = { id: 42, estimate_minutes: null, story_points: null };
    itemDetailStore.saving = false;
    items.update.mockReset();
    items.update.mockImplementation(async (_id, data) => ({ ...data }));
    items.transition.mockReset();
    items.getAvailableStatusTransitions.mockReset();
  });

  it('persists estimate_minutes updates', async () => {
    await itemDetailStore.saveField('estimate_minutes', 240);

    expect(items.update).toHaveBeenCalledWith(42, { estimate_minutes: 240 });
    expect(itemDetailStore.item.estimate_minutes).toBe(240);
  });

  it('persists estimate clears', async () => {
    itemDetailStore.item = { id: 42, estimate_minutes: 240 };

    await itemDetailStore.saveField('estimate_minutes', null);

    expect(items.update).toHaveBeenCalledWith(42, { estimate_minutes: null });
    expect(itemDetailStore.item.estimate_minutes).toBeNull();
  });

  it('refreshes available transitions after changing status', async () => {
    const nextTransitions = [{ id: 3, name: 'Done' }];
    itemDetailStore.item = { id: 42, status_id: 1 };
    itemDetailStore.availableStatusTransitions = [{ id: 2, name: 'In Progress' }];
    items.transition.mockResolvedValue({ id: 42, status_id: 2 });
    items.getAvailableStatusTransitions.mockResolvedValue({
      available_transitions: nextTransitions,
    });

    await itemDetailStore.saveField('status_id', 2);

    expect(items.transition).toHaveBeenCalledWith(42, 2);
    expect(items.getAvailableStatusTransitions).toHaveBeenCalledWith(42, {});
    expect(itemDetailStore.item.status_id).toBe(2);
    expect(itemDetailStore.availableStatusTransitions).toEqual(nextTransitions);
  });

  it('persists a team assignment and optimistically updates the team name', async () => {
    itemDetailStore.item = { id: 42, team_id: null, team_name: null };

    await itemDetailStore.saveField('team', 7, null, null, 'Platform');

    expect(items.update).toHaveBeenCalledWith(42, { team_id: 7 });
    expect(itemDetailStore.item.team_id).toBe(7);
    expect(itemDetailStore.item.team_name).toBe('Platform');
  });

  it('clears the team assignment', async () => {
    itemDetailStore.item = { id: 42, team_id: 7, team_name: 'Platform' };

    await itemDetailStore.saveField('team', null, null, null, null);

    expect(items.update).toHaveBeenCalledWith(42, { team_id: null });
    expect(itemDetailStore.item.team_id).toBeNull();
  });
});

describe('itemDetailStore optional item data', () => {
  beforeEach(() => {
    itemDetailStore.reset();
    itemDetailStore.itemId = 42;
    itemDetailStore.item = { id: 42 };
    items.getAll.mockReset();
    links.getForItem.mockReset();
    time.worklogs.getByItem.mockReset();
    customerOrganisations.getAll.mockReset();
    workspaces.getAll.mockReset();
    getDiagrams.mockReset();
  });

  it('single-flights worklogs for the open item', async () => {
    let resolveWorklogs;
    time.worklogs.getByItem.mockReturnValue(
      new Promise((resolve) => {
        resolveWorklogs = resolve;
      })
    );

    const first = itemDetailStore.loadWorklogs();
    const second = itemDetailStore.loadWorklogs();
    expect(time.worklogs.getByItem).toHaveBeenCalledTimes(1);

    resolveWorklogs([{ id: 9, duration_minutes: 30 }]);
    await Promise.all([first, second]);

    expect(itemDetailStore.timeWorklogs).toEqual([{ id: 9, duration_minutes: 30 }]);
  });

  it('keeps the complete previous worklog result when a later page fails', async () => {
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    itemDetailStore.timeWorklogs = [{ id: 7, duration_minutes: 45 }];
    time.worklogs.getByItem.mockRejectedValue(new Error('page 2 failed'));

    await itemDetailStore.loadWorklogs({ force: true });

    expect(itemDetailStore.timeWorklogs).toEqual([{ id: 7, duration_minutes: 45 }]);
    expect(errorSpy).toHaveBeenCalledWith('Failed to load worklogs:', expect.any(Error));
  });

  it('aborts a superseded worklog page drain and ignores its stale result', async () => {
    let resolveFirst;
    let firstSignal;
    time.worklogs.getByItem.mockImplementation((itemId, { signal }) => {
      if (itemId === 42) {
        firstSignal = signal;
        return new Promise((resolve) => {
          resolveFirst = resolve;
        });
      }
      return Promise.resolve([{ id: 200, item_id: itemId, duration_minutes: 60 }]);
    });

    const firstLoad = itemDetailStore.loadWorklogs();
    itemDetailStore.itemId = 99;
    itemDetailStore.item = { id: 99 };
    const secondLoad = itemDetailStore.loadWorklogs();
    await secondLoad;
    resolveFirst([{ id: 100, item_id: 42, duration_minutes: 30 }]);
    await firstLoad;

    expect(firstSignal.aborted).toBe(true);
    expect(itemDetailStore.timeWorklogs).toEqual([{ id: 200, item_id: 99, duration_minutes: 60 }]);
  });

  it('loads time-modal-only picker data once', async () => {
    customerOrganisations.getAll.mockResolvedValue([{ id: 1 }]);
    items.getAll.mockResolvedValue({ data: [{ id: 2 }] });
    workspaces.getPage.mockResolvedValue(
      { data: [{ id: 3 }], pagination: { total: 1 } }
    );

    await Promise.all([itemDetailStore.loadTimeModalData(), itemDetailStore.loadTimeModalData()]);
    await itemDetailStore.loadTimeModalData();

    expect(customerOrganisations.getAll).toHaveBeenCalledTimes(1);
    expect(items.getAll).toHaveBeenCalledTimes(1);
    expect(workspaces.getPage).toHaveBeenCalledTimes(1);
    expect(itemDetailStore.customers).toEqual([{ id: 1 }]);
    expect(itemDetailStore.workItems).toEqual([{ id: 2 }]);
    expect(itemDetailStore.workspaces).toEqual([{ id: 3 }]);
  });

  it('single-flights diagram requests and caches the result', async () => {
    let resolveDiagrams;
    getDiagrams.mockReturnValue(
      new Promise((resolve) => {
        resolveDiagrams = resolve;
      })
    );

    const first = itemDetailStore.loadDiagrams();
    const second = itemDetailStore.loadDiagrams();
    expect(getDiagrams).toHaveBeenCalledTimes(1);
    expect(itemDetailStore.diagramsLoaded).toBe(false);

    resolveDiagrams([{ id: 5, name: 'Architecture' }]);
    await Promise.all([first, second]);

    expect(itemDetailStore.diagramsLoaded).toBe(true);
    expect(itemDetailStore.diagrams).toEqual([{ id: 5, name: 'Architecture' }]);

    await itemDetailStore.loadDiagrams();
    expect(getDiagrams).toHaveBeenCalledTimes(1);
  });

  it('refreshes links without reloading the complete item', async () => {
    links.getForItem.mockResolvedValue({
      outgoing: [{ id: 1 }],
      incoming: [{ id: 2 }],
    });

    await itemDetailStore.loadLinks();

    expect(links.getForItem).toHaveBeenCalledWith(
      'items',
      42,
      expect.objectContaining({ signal: expect.any(AbortSignal) })
    );
    expect(itemDetailStore.itemLinks).toEqual([{ id: 1 }, { id: 2 }]);
  });
});

describe('itemDetailStore.saveField rapid edits', () => {
  beforeEach(() => {
    itemDetailStore.reset();
    itemDetailStore.item = {
      id: 42,
      estimate_minutes: null,
      story_points: null,
      custom_field_values: { 7: '' },
    };
    itemDetailStore.saving = false;
    items.update.mockReset();
    items.update.mockImplementation(async (_id, data) => ({ ...data }));
  });

  it('persists a second custom-field change made while the first save is in flight', async () => {
    let resolveFirst;
    items.update.mockImplementationOnce(
      (_id, data) =>
        new Promise((resolve) => {
          resolveFirst = () => resolve({ ...data });
        })
    );

    // First save starts and holds the API in flight.
    const first = itemDetailStore.saveField('custom_field_7', 'first');
    // Second keystroke lands while saving; must be queued, not dropped.
    const second = itemDetailStore.saveField('custom_field_7', 'second');

    resolveFirst();
    await Promise.all([first, second]);

    expect(items.update).toHaveBeenCalledTimes(2);
    expect(items.update).toHaveBeenNthCalledWith(1, 42, {
      custom_field_values: { 7: 'first' },
    });
    expect(items.update).toHaveBeenNthCalledWith(2, 42, {
      custom_field_values: { 7: 'second' },
    });
    expect(itemDetailStore.item.custom_field_values[7]).toBe('second');
  });

  it('captures the editing-draft value when the change path passes no direct value', async () => {
    itemDetailStore.startEditing('custom_field_7');
    let resolveFirst;
    items.update.mockImplementationOnce(
      (_id, data) =>
        new Promise((resolve) => {
          resolveFirst = () => resolve({ ...data });
        })
    );

    // First keystroke saves with an explicit value and holds in flight.
    const first = itemDetailStore.saveField('custom_field_7', 'draft v1');
    // Second keystroke updates the shared editing draft, then saves without a
    // direct value; the queued replay must carry the draft's latest content.
    itemDetailStore.editing.customFields.values[7] = 'draft v2';
    const second = itemDetailStore.saveField('custom_field_7');

    resolveFirst();
    await Promise.all([first, second]);

    expect(items.update).toHaveBeenCalledTimes(2);
    expect(items.update).toHaveBeenNthCalledWith(1, 42, {
      custom_field_values: { 7: 'draft v1' },
    });
    expect(items.update).toHaveBeenLastCalledWith(42, {
      custom_field_values: { 7: 'draft v2' },
    });
  });

  it('drains pending saves for different fields after an in-flight save completes', async () => {
    itemDetailStore.item = { ...itemDetailStore.item, story_points: 1 };
    let resolveFirst;
    items.update.mockImplementationOnce(
      (_id, data) =>
        new Promise((resolve) => {
          resolveFirst = () => resolve({ ...data });
        })
    );

    const first = itemDetailStore.saveField('custom_field_7', 'later');
    const second = itemDetailStore.saveField('story_points', 8);

    resolveFirst();
    await Promise.all([first, second]);

    expect(items.update).toHaveBeenCalledTimes(2);
    expect(itemDetailStore.item.story_points).toBe(8);
    expect(itemDetailStore.item.custom_field_values[7]).toBe('later');
  });

  it('resolves screen fields from the summary screen context without extra config requests', async () => {
    // Screens are resolved server-side and shipped with the detail summary;
    // no client-side config-set resolution may run alongside it.
    items.getDetailSummary.mockResolvedValue(
      detailSummary(
        {
          id: 42,
          workspace_id: 1,
          title: 'Custom screen item',
          parent_id: null,
          item_type_id: 9,
          request_type_id: null,
        },
        {
          screen_context: {
            edit: { id: 31, fields: [{ field_type: 'system', field_identifier: 'priority' }] },
            view: { id: 33, fields: [{ field_type: 'system', field_identifier: 'estimate' }] },
          },
        }
      )
    );

    await itemDetailStore.loadItem(1, 42);

    expect(workspaces.getEffectiveConfig).not.toHaveBeenCalled();
    expect(configurationSets.getAll).not.toHaveBeenCalled();
    expect(screens.get).not.toHaveBeenCalled();
    // Edit and view screens differ, so the visible system fields are the
    // union of both screens.
    expect(itemDetailStore.workspaceScreenSystemFields).toEqual(
      expect.arrayContaining(['estimate', 'priority', 'status'])
    );
  });

});

describe('itemDetailStore test case modal workspace scope', () => {
  beforeEach(() => {
    itemDetailStore.reset();
  });

  it('stores the workspace id supplied with the test case and clears it on close', () => {
    itemDetailStore.openTestCaseModal(134, 11);

    expect(itemDetailStore.selectedTestCaseId).toBe(134);
    expect(itemDetailStore.selectedTestCaseWorkspaceId).toBe(11);
    expect(itemDetailStore.showTestCaseModal).toBe(true);

    itemDetailStore.closeTestCaseModal();

    expect(itemDetailStore.selectedTestCaseId).toBeNull();
    expect(itemDetailStore.selectedTestCaseWorkspaceId).toBeNull();
    expect(itemDetailStore.showTestCaseModal).toBe(false);
  });

  it('defaults the workspace id to null when the caller omits it', () => {
    itemDetailStore.openTestCaseModal(134);

    expect(itemDetailStore.selectedTestCaseWorkspaceId).toBeNull();
  });
});
