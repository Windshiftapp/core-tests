import { render, screen, waitFor } from '@testing-library/svelte';
import { writable } from 'svelte/store';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { items } from '../../api/items.js';
import { configurationSets, customFields, hierarchyLevels, itemTypes, linkTypes, links, priorities, screens } from '../../api/configuration.js';
import { attachments, getDiagrams } from '../../api/misc.js';
import { time } from '../../api/time.js';
import { customerOrganisations } from '../../api/portal.js';
import { workspaces } from '../../api/workspaces.js';
import { iterations, milestones } from '../../api/milestones.js';
import { requestTypes } from '../../api/channels.js';
import { actions } from '../../api/actions.js';










vi.mock('../../api/items.js', () => ({
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

vi.mock('../../api/configuration.js', () => ({
  links: {
        create: vi.fn(),
        delete: vi.fn(),
        getForItem: vi.fn(),
      },
  linkTypes: { getAll: vi.fn() },
  customFields: { getAll: vi.fn() },
  configurationSets: { get: vi.fn(), getAll: vi.fn() },
  priorities: { getAll: vi.fn() },
  itemTypes: { getAll: vi.fn() },
  hierarchyLevels: { getAll: vi.fn() },
  screens: { get: vi.fn() },
}))

vi.mock('../../api/misc.js', () => ({
  attachments: {
        getByItem: vi.fn().mockResolvedValue({ data: [], pagination: null }),
      },
  getDiagrams: vi.fn(),
}))

vi.mock('../../api/time.js', () => ({
  time: {
        projects: { getByWorkspace: vi.fn() },
        worklogs: { getByItem: vi.fn() },
      },
}))

vi.mock('../../api/portal.js', () => ({
  customerOrganisations: { getAll: vi.fn() },
}))

vi.mock('../../api/workspaces.js', () => ({
  workspaces: {
        get: vi.fn(),
        getAll: vi.fn(),
        getPage: vi.fn(),
        getBootstrap: vi.fn(),
        getEffectiveConfig: vi.fn(),
      },
}))

vi.mock('../../api/milestones.js', () => ({
  milestones: { getAll: vi.fn() },
  iterations: { getAll: vi.fn() },
  milestoneCategories: { getAll: vi.fn().mockResolvedValue([]) },
}))

vi.mock('../../api/channels.js', () => ({
  requestTypes: { getFields: vi.fn() },
}))

vi.mock('../../api/actions.js', () => ({
  actions: { getAll: vi.fn() },
}));

vi.mock('../../router.js', () => ({
  navigate: vi.fn(),
  currentRoute: writable({ path: '/', view: null, params: {}, query: {} }),
}));

vi.mock('../../stores/itemDetailStore.svelte.js', async () => {
  const { itemDetailStore } = await import('../../stores/itemDetailStore.svelte.js');
  return {
    itemDetailStore,
  };
})

vi.mock('../../stores/workspacePermissions.svelte.js', async () => {
  const { itemDetailStore } = await import('../../stores/itemDetailStore.svelte.js');
  return {
    workspacePermissions: {
          hasPermission: () => true,
          canEdit: () => true,
          canDelete: () => true,
          canCreate: () => true,
        },
  };
})

vi.mock('../../stores/attachmentStatus.svelte.js', async () => {
  const { itemDetailStore } = await import('../../stores/itemDetailStore.svelte.js');
  return {
    attachmentStatus: { enabled: false, load: vi.fn().mockResolvedValue(undefined) },
  };
});

vi.mock('../../stores/i18n.svelte.js', () => ({ t: (key) => key }));
vi.mock('../../stores/toasts.svelte.js', () => ({
  addToast: vi.fn(),
  successToast: vi.fn(),
  errorToast: vi.fn(),
  infoToast: vi.fn(),
}));
vi.mock('../../stores/timerStore.svelte.js', () => ({
  timerStore: { activeTimer: null, getCurrent: () => null },
}));
vi.mock('../../stores/notifications.js', () => ({
  notificationActions: { markItemAsRead: vi.fn() },
}));
vi.mock('../../stores/agentRuns.svelte.js', () => ({
  agentRuns: { subscribe: () => () => {} },
}));
vi.mock('../../composables/useItemAttachments.svelte.js', () => ({
  useItemAttachments: () => ({
    attachments: [],
    pagination: null,
    loadSettings: vi.fn().mockResolvedValue(undefined),
    isEnabled: () => false,
    load: vi.fn().mockResolvedValue(undefined),
  }),
}));
vi.mock('../../composables/useItemEventStream.svelte.js', () => ({
  useItemEventStream: () => ({ connected: false }),
}));
vi.mock('../../utils/contextCommands.js', () => ({
  registerContextCommands: vi.fn(),
  unregisterContextCommands: vi.fn(),
  createContextCommand: vi.fn(),
  COMMAND_PRIORITIES: {},
}));
vi.mock('../../utils/clipboard.js', () => ({ copyToClipboard: vi.fn() }));
vi.mock('../../utils/keyboardShortcuts.js', () => ({
  getShortcut: () => '',
  matchesShortcut: () => false,
  isTypingInField: () => false,
}));
vi.mock('runed', () => ({ useEventListener: vi.fn() }));

vi.mock('../../dialogs/Modal.svelte', async () => ({
  default: (await import('./ItemDetailChildStub.svelte')).default,
}));
vi.mock('../../dialogs/FormModal.svelte', async () => ({
  default: (await import('./ItemDetailChildStub.svelte')).default,
}));
vi.mock('../../dialogs/DeleteItemDialog.svelte', async () => ({
  default: (await import('./ItemDetailChildStub.svelte')).default,
}));
vi.mock('../../dialogs/LinkItemModal.svelte', async () => ({
  default: (await import('./ItemDetailChildStub.svelte')).default,
}));
vi.mock('../../dialogs/AIViewModal.svelte', async () => ({
  default: (await import('./ItemDetailChildStub.svelte')).default,
}));
vi.mock('../../dialogs/AIConfirmModal.svelte', async () => ({
  default: (await import('./ItemDetailChildStub.svelte')).default,
}));
vi.mock('../../dialogs/TimeLogModal.svelte', async () => ({
  default: (await import('./TimeLogModalStub.svelte')).default,
}));
vi.mock('../../dialogs/TestCaseViewModal.svelte', async () => ({
  default: (await import('./ItemDetailChildStub.svelte')).default,
}));
vi.mock('./CatchMeUpBriefing.svelte', async () => ({
  default: (await import('./ItemDetailChildStub.svelte')).default,
}));
vi.mock('./FindSimilarResults.svelte', async () => ({
  default: (await import('./ItemDetailChildStub.svelte')).default,
}));
vi.mock('./ItemMoveWorkspaceDialog.svelte', async () => ({
  default: (await import('./ItemDetailChildStub.svelte')).default,
}));
vi.mock('./ItemMergeDialog.svelte', async () => ({
  default: (await import('./ItemDetailChildStub.svelte')).default,
}));
vi.mock('./ItemSplitDialog.svelte', async () => ({
  default: (await import('./ItemDetailChildStub.svelte')).default,
}));
vi.mock('../../editors/RecurrenceEditor.svelte', async () => ({
  default: (await import('./ItemDetailChildStub.svelte')).default,
}));
vi.mock('../../components/Button.svelte', async () => ({
  default: (await import('./ItemDetailChildStub.svelte')).default,
}));
vi.mock('../../components/NativeSelect.svelte', async () => ({
  default: (await import('./ItemDetailChildStub.svelte')).default,
}));
vi.mock('./ItemDetailContent.svelte', async () => ({
  default: (await import('./ItemDetailContentStub.svelte')).default,
}));


const { itemDetailStore } = await import('../../stores/itemDetailStore.svelte.js');
const { workspaceDataStore } = await import('../../stores/workspaceDataStore.svelte.js');
const { navigate } = await import('../../router.js');
const { default: ItemDetail } = await import('./ItemDetail.svelte');

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
  workspaces.getEffectiveConfig.mockResolvedValue({ edit_screen: null, view_screen: null });
  linkTypes.getAll.mockResolvedValue([]);
  links.getForItem.mockResolvedValue({ outgoing: [], incoming: [] });
  customFields.getAll.mockResolvedValue({ data: [] });
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

function summaryItem(id) {
  return {
    id,
    workspace_id: 1,
    title: `Item ${id}`,
    parent_id: null,
    item_type_id: null,
    request_type_id: null,
  };
}

describe('ItemDetail stale-load ownership', () => {
  let summaryCalls;

  beforeEach(() => {
    itemDetailStore.reset();
    workspaceDataStore.reset();
    navigate.mockClear();
    summaryCalls = [];
    mockSuccessfulRelatedLoads();

    // Defer every key-addressed summary so the test controls resolution order
    // and can observe abort propagation.
    items.getDetailSummaryByKey.mockImplementation(
      (_workspaceKey, itemNumber, { signal } = {}) => {
        const entry = { aborted: false };
        entry.promise = new Promise((resolve, reject) => {
          entry.resolve = () => resolve(detailSummary(summaryItem(itemNumber)));
          entry.reject = reject;
        });
        signal?.addEventListener(
          'abort',
          () => {
            entry.aborted = true;
            entry.reject(Object.assign(new Error('aborted'), { name: 'AbortError' }));
          },
          { once: true }
        );
        summaryCalls.push(entry);
        return entry.promise;
      }
    );
  });

  it('does not canonicalize a stale item after a superseded key-route load', async () => {
    const baseProps = {
      workspaceKey: 'WI',
      itemNumber: 101,
      itemId: 101,
      workspaceId: null,
      canonicalizeKeyRoute: true,
      tab: 'comments',
    };

    const { rerender } = render(ItemDetail, { props: { ...baseProps } });

    await waitFor(() => expect(summaryCalls).toHaveLength(1));
    summaryCalls[0].resolve();
    await waitFor(() =>
      expect(screen.getByTestId('item-detail-ready')).toHaveTextContent('Item 101')
    );
    await waitFor(() =>
      expect(navigate).toHaveBeenCalledWith('/workspaces/1/items/101', { replace: true })
    );
    const callsAfterInitial = navigate.mock.calls.length;

    // Navigate to 102, then to 103 before 102 resolves. Starting 103 aborts 102.
    await rerender({ ...baseProps, itemNumber: 102, itemId: 102 });
    await waitFor(() => expect(summaryCalls).toHaveLength(2));
    await rerender({ ...baseProps, itemNumber: 103, itemId: 103 });
    await waitFor(() => expect(summaryCalls).toHaveLength(3));
    await waitFor(() => expect(summaryCalls[1].aborted).toBe(true));

    // The current destination still resolves and canonicalizes to 103.
    summaryCalls[2].resolve();
    await waitFor(() =>
      expect(navigate).toHaveBeenLastCalledWith('/workspaces/1/items/103', { replace: true })
    );
    expect(screen.getByTestId('item-detail-ready')).toHaveTextContent('Item 103');

    // The superseded 102 continuation must never have canonicalized to 101.
    expect(navigate.mock.calls.filter(([path]) => path === '/workspaces/1/items/101')).toHaveLength(
      1
    );
    expect(navigate.mock.calls.length).toBe(callsAfterInitial + 1);
  });

  it('reloads when the workspace key changes but the item number is unchanged', async () => {
    // W1 item 5 resolves to global id 5; W2 item 5 resolves to a different item.
    items.getDetailSummaryByKey.mockImplementation(
      (workspaceKey, itemNumber, { signal } = {}) => {
        const entry = { aborted: false, workspaceKey, itemNumber };
        entry.promise = new Promise((resolve, reject) => {
          entry.resolve = () =>
            resolve(
              detailSummary({
                ...summaryItem(workspaceKey === 'W1' ? 5 : 20),
                workspace_id: workspaceKey === 'W1' ? 1 : 2,
                title: `${workspaceKey} item ${itemNumber}`,
              })
            );
          entry.reject = reject;
        });
        signal?.addEventListener(
          'abort',
          () => {
            entry.aborted = true;
            entry.reject(Object.assign(new Error('aborted'), { name: 'AbortError' }));
          },
          { once: true }
        );
        summaryCalls.push(entry);
        return entry.promise;
      }
    );

    const baseProps = {
      workspaceKey: 'W1',
      itemNumber: 5,
      itemId: 5,
      workspaceId: null,
      canonicalizeKeyRoute: true,
      tab: 'comments',
    };
    const { rerender } = render(ItemDetail, { props: { ...baseProps } });

    await waitFor(() => expect(summaryCalls).toHaveLength(1));
    summaryCalls[0].resolve();
    await waitFor(() =>
      expect(navigate).toHaveBeenCalledWith('/workspaces/1/items/5', { replace: true })
    );

    // Same item number in another workspace. The resolved id (5) equals the new
    // item number, so only the workspace identity tells the two apart.
    await rerender({ ...baseProps, workspaceKey: 'W2', itemId: 5 });

    await waitFor(() => expect(summaryCalls).toHaveLength(2));
    expect(summaryCalls[1].workspaceKey).toBe('W2');
    summaryCalls[1].resolve();
    await waitFor(() =>
      expect(navigate).toHaveBeenLastCalledWith('/workspaces/2/items/20', { replace: true })
    );
    expect(screen.getByTestId('item-detail-ready')).toHaveTextContent('W2 item 5');
  });
});

describe('ItemDetail time log modal rendering', () => {
  beforeEach(() => {
    itemDetailStore.reset();
    workspaceDataStore.reset();
    navigate.mockClear();
    mockSuccessfulRelatedLoads();
    items.getDetailSummaryByKey.mockResolvedValue(detailSummary(summaryItem(1)));
  });

  it('mounts the time log modal from the item detail modal', async () => {
    // Regression: TimeLogModal was rendered only in the full-page branch, so
    // "Log time" did nothing while the item detail was shown as a modal.
    render(ItemDetail, {
      props: {
        workspaceKey: 'WI',
        itemNumber: 1,
        itemId: 1,
        workspaceId: 1,
        isModal: true,
        tab: 'time',
      },
    });

    expect(screen.queryByTestId('time-log-modal-stub')).not.toBeInTheDocument();

    itemDetailStore.showTimeLogModal = true;
    await waitFor(() =>
      expect(screen.getByTestId('time-log-modal-stub')).toBeInTheDocument()
    );
  });
});
