import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { tick } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { getHierarchy, getLevelCounts, preferencesState } = vi.hoisted(() => ({
  getHierarchy: vi.fn(),
  getLevelCounts: vi.fn(),
  preferencesState: { viewState: {} },
}));

vi.mock('../../stores/collectionContext.js', async () => {
  const { collectionTreeStore } = await import('./collectionTreeStoreMock.svelte.js');
  return { collectionStore: collectionTreeStore, scheduleCollectionReconciliation: vi.fn() };
});
vi.mock('../../stores/auth.svelte.js', () => ({
  authStore: { currentUser: { id: 1 } },
}));
vi.mock('../../stores/workspacePermissions.svelte.js', () => ({
  workspacePermissions: { canEdit: () => true, canAdminWorkspace: () => true },
}));

vi.mock('../../stores/itemTestCaseLinksStore.svelte.js', () => ({
  itemTestCaseLinksStore: {
    initialize: vi.fn(),
    loadForItems: vi.fn().mockResolvedValue(undefined),
    get: vi.fn().mockReturnValue(null),
  },
}));

vi.mock('../../stores/workspaceDataStore.svelte.js', () => ({
  workspaceDataStore: {
    workspace: { id: 1, name: 'Windshift', key: 'WS' },
    itemTypes: [
      { id: 10, name: 'Task', hierarchy_level: 3 },
      { id: 11, name: 'Bug', hierarchy_level: 3 },
      { id: 12, name: 'Sub-task', hierarchy_level: -1 },
    ],
    statuses: [],
    statusCategories: [],
    priorities: [],
    users: [],
    milestones: [],
    iterations: [],
    projects: [],
    customFieldDefinitions: [],
    initialized: false,
    workspaceId: null,
    initialize: vi.fn().mockResolvedValue(undefined),
    initializeGlobal: vi.fn().mockResolvedValue(undefined),
    hydrateUsers: vi.fn().mockResolvedValue(undefined),
  },
}));
vi.mock('../../api/items.js', () => ({
  items: { getHierarchy, getLevelCounts },
}));
vi.mock('../../stores/collectionPreferences.svelte.js', () => {
  const scopeId = (ws, col) => (col ? `collection-${col}` : `workspace-${ws ?? 'global'}`);
  return {
    collectionPreferences: {
      viewStateValue(ws, col, view, key) {
        const state = preferencesState.viewState[scopeId(ws, col)]?.[view];
        return state && Object.hasOwn(state, key) ? state[key] : undefined;
      },
      patchViewState(ws, col, view, patch) {
        const scope = scopeId(ws, col);
        const currentScope = preferencesState.viewState[scope] ?? {};
        preferencesState.viewState = {
          ...preferencesState.viewState,
          [scope]: {
            ...currentScope,
            [view]: { ...(currentScope[view] ?? {}), ...patch },
          },
        };
      },
    },
  };
});
vi.mock('../../stores/workspaceGradient.svelte.js', () => ({
  useGradientStyles: () => ({
    backgroundStyle: '',
    contextVars: '',
    glassStyle: () => '',
    glassTextStyle: '',
    glassSubtleTextStyle: '',
  }),
  loadWorkspaceGradient: vi.fn().mockResolvedValue(undefined),
}));
vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key, params = {}) => {
    const strings = {
      'collectionHierarchy.selectLevel': 'Select hierarchy level',
      'collectionHierarchy.levelN': 'Level {level}',
      'collectionHierarchy.itemsCount': '{count} items',
      'collectionTree.tree': 'Tree',
      'collectionTree.expandAll': 'Expand All',
      'collectionTree.collapseAll': 'Collapse All',
      'collectionTree.noWorkItemsYet': 'No work items yet',
      'collectionTree.createFirstWorkItem': 'Create your first work item.',
      'collectionTree.loading': 'Loading...',
      'common.loadMore': 'Load more',
      'common.loading': 'Loading',
      'common.remaining': 'remaining',
    };
    let text = strings[key] ?? key;
    for (const [name, value] of Object.entries(params)) {
      text = text.replaceAll(`{${name}}`, String(value));
    }
    return text;
  },
}));
// Unrelated view chrome is outside the hierarchy contract.
vi.mock('./SubFilterBar.svelte', () => import('./EmptyBacklogChild.svelte'));
vi.mock('../../dialogs/TestCaseViewModal.svelte', () => import('./EmptyBacklogChild.svelte'));

import CollectionTree from './CollectionTree.svelte';
import { collectionTreeStore } from './collectionTreeStoreMock.svelte.js';

function item(id, parentId, extra = {}) {
  return {
    id,
    parent_id: parentId,
    title: `Item ${id}`,
    workspace_item_number: id,
    workspace_id: 1,
    workspace_key: 'WS',
    status_id: 1,
    frac_index: String(id),
    ...extra,
  };
}

function group(rootId, items, { total = items.length, hasMore = false } = {}) {
  return { root_id: rootId, total, has_more: hasMore, items };
}

function rowIndent(testId) {
  const id = testId.replace('tree-row-', '');
  return screen.getByTestId(`tree-indent-${id}`).style.paddingLeft;
}

beforeEach(() => {
  getHierarchy.mockResolvedValue([]);
  getLevelCounts.mockResolvedValue([]);
});

afterEach(() => {
  cleanup();
  localStorage.clear();
  collectionTreeStore.items = [];
  collectionTreeStore.itemsPagination = null;
  collectionTreeStore.collectionTotal = null;
  collectionTreeStore.itemsHasMore = false;
  collectionTreeStore.itemsLoadingMore = false;
  collectionTreeStore.itemsRemainingCount = 0;
  collectionTreeStore.loadMoreItems.mockReset();
  collectionTreeStore.ensureLevelCounts.mockClear();
  collectionTreeStore.setHierarchyLevel.mockClear();
  collectionTreeStore.levelCounts = [];
  collectionTreeStore.levelCountsLoading = false;
  collectionTreeStore.hierarchyLevel = null;
  collectionTreeStore.effectiveSubFilterQL = '';
  preferencesState.viewState = {};
  getHierarchy.mockReset();
  getLevelCounts.mockReset().mockResolvedValue([]);
});

describe('CollectionTree hierarchy loading', () => {
  it('renders the level-scoped base rows and nests their eagerly loaded descendants', async () => {
    getHierarchy.mockResolvedValue([
      group(1, [item(20, 1, { has_children: true }), item(21, 1), item(30, 20)]),
    ]);
    collectionTreeStore.items = [item(1, null, { has_children: true })];

    render(CollectionTree, { workspaceId: 1 });

    expect(await screen.findByTestId('tree-row-1')).toBeInTheDocument();
    expect(await screen.findByTestId('tree-row-20')).toBeInTheDocument();
    expect(screen.getByTestId('tree-row-21')).toBeInTheDocument();
    // Deeper descendants are collapsed until their parent is expanded.
    expect(screen.queryByTestId('tree-row-30')).not.toBeInTheDocument();
    await fireEvent.click(screen.getByTestId('tree-expand-20'));
    expect(await screen.findByTestId('tree-row-30')).toBeInTheDocument();
    expect(rowIndent('tree-row-20')).toBe('1.5rem');
    expect(rowIndent('tree-row-30')).toBe('3rem');
    expect(getHierarchy).toHaveBeenCalledTimes(1);
  });

  it('shows a parent reference chip when the parent is not drawn', async () => {
    getHierarchy.mockResolvedValue([group(1, [])]);
    // Base root whose real parent (id 99) is above the picked level.
    collectionTreeStore.items = [
      item(1, 99, {
        has_children: false,
        parent_title: 'Parent epic',
        parent_workspace_item_number: 99,
      }),
    ];

    render(CollectionTree, { workspaceId: 1 });

    expect(await screen.findByTestId('tree-parent-1')).toBeInTheDocument();
    expect(screen.getByTestId('tree-parent-1')).toHaveTextContent('WS-99');
  });

  it('draws a depth guide only on nested rows', async () => {
    getHierarchy.mockResolvedValue([group(1, [item(20, 1)])]);
    collectionTreeStore.items = [item(1, null, { has_children: true })];

    render(CollectionTree, { workspaceId: 1 });

    const rootIndent = await screen.findByTestId('tree-indent-1');
    expect(rootIndent.getAttribute('style') ?? '').not.toContain('repeating-linear-gradient');
    expect(rootIndent.style.paddingLeft).toBe('');

    const childIndent = screen.getByTestId('tree-indent-20');
    expect(childIndent.style.paddingLeft).toBe('1.5rem');
    expect(childIndent.getAttribute('style')).toContain('repeating-linear-gradient');
  });

  it('connects an expanded parent to its children with a branch line', async () => {
    getHierarchy.mockResolvedValue([group(1, [item(20, 1)])]);
    collectionTreeStore.items = [item(1, null, { has_children: true })];

    render(CollectionTree, { workspaceId: 1 });

    const expanded = await screen.findByTestId('tree-indent-1');
    expect(expanded.getAttribute('style')).toContain(
      'background-image: linear-gradient(var(--ds-border), var(--ds-border))',
    );

    await fireEvent.click(screen.getByTestId('tree-expand-1'));

    expect(screen.queryByTestId('tree-row-20')).not.toBeInTheDocument();
    const collapsed = screen.getByTestId('tree-indent-1');
    expect(collapsed.getAttribute('style') ?? '').not.toContain('linear-gradient');
  });

  it('renders the parent chip in the key column so it cannot push the title right', async () => {
    getHierarchy.mockResolvedValue([group(1, [])]);
    collectionTreeStore.items = [
      item(1, 99, {
        item_type_id: 10,
        parent_title: 'Parent epic',
        parent_workspace_item_number: 99,
      }),
    ];

    render(CollectionTree, { workspaceId: 1 });

    const chip = await screen.findByTestId('tree-parent-1');
    const title = screen.getByTestId('workspace-item-title-1');
    expect(title.contains(chip)).toBe(false);
    expect(chip.compareDocumentPosition(title) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it('clamps the title cell to two lines', async () => {
    getHierarchy.mockResolvedValue([group(1, [])]);
    collectionTreeStore.items = [item(1, null)];

    render(CollectionTree, { workspaceId: 1 });

    const title = await screen.findByTestId('workspace-item-title-1');
    expect(title.querySelector('span')?.getAttribute('style')).toContain('-webkit-line-clamp: 2');
  });

  it('offers a per-root Load more when a subtree exceeded the cap', async () => {
    getHierarchy.mockImplementation(({ perRootOffset }) =>
      Promise.resolve([
        perRootOffset === 0
          ? group(1, [item(20, 1), item(21, 1)], { total: 4, hasMore: true })
          : group(1, [item(22, 1), item(23, 1)], { total: 4, hasMore: false }),
      ])
    );
    collectionTreeStore.items = [item(1, null, { has_children: true })];

    render(CollectionTree, { workspaceId: 1 });

    const button = await screen.findByTestId('tree-load-more-button-1');
    expect(button).toHaveTextContent('Load more');
    expect(button).toHaveTextContent('2 remaining');

    await fireEvent.click(button);

    expect(await screen.findByTestId('tree-row-22')).toBeInTheDocument();
    expect(screen.queryByTestId('tree-load-more-button-1')).not.toBeInTheDocument();
  });

  it('offers a base-level Load more when the level page is truncated', async () => {
    getHierarchy.mockResolvedValue([group(1, [])]);
    collectionTreeStore.items = [item(1, null)];
    collectionTreeStore.itemsHasMore = true;
    collectionTreeStore.itemsRemainingCount = 49;

    render(CollectionTree, { workspaceId: 1 });

    const button = await screen.findByTestId('tree-load-more-button');
    expect(button).toHaveTextContent('Load more');
    expect(button).toHaveTextContent('49 remaining');

    await fireEvent.click(button);
    expect(collectionTreeStore.loadMoreItems).toHaveBeenCalledTimes(1);
  });

  it('keeps the default key width when no row shows a parent reference', async () => {
    getHierarchy.mockResolvedValue([group(1, [])]);
    collectionTreeStore.items = [item(1, null)];

    render(CollectionTree, { workspaceId: 1 });

    const row = await screen.findByTestId('tree-row-1');
    expect(row.style.gridTemplateColumns).toContain('5rem');
    expect(row.style.gridTemplateColumns).not.toContain('10rem');
  });

  it('widens the key column when a row shows a parent reference', async () => {
    getHierarchy.mockResolvedValue([group(1, [])]);
    collectionTreeStore.items = [
      item(1, 99, { parent_title: 'Parent epic', parent_workspace_item_number: 99 }),
    ];

    render(CollectionTree, { workspaceId: 1 });

    const row = await screen.findByTestId('tree-row-1');
    await waitFor(() => expect(row.style.gridTemplateColumns).toContain('10rem'));
  });

  it('renders one level option per hierarchy level with every item type in it', async () => {
    collectionTreeStore.levelCounts = [{ level: 3, name: 'Task', total: 4 }];
    collectionTreeStore.items = [item(1, null)];

    render(CollectionTree, { workspaceId: 1 });

    const trigger = await screen.findByTestId('hierarchy-level-trigger');
    expect(trigger).toHaveTextContent('Task, Bug');

    await fireEvent.click(trigger);
    const option = await screen.findByTestId('hierarchy-level-option-3');
    // Sub-task (-1) is never offered as a level.
    expect(screen.queryByTestId('hierarchy-level-option--1')).not.toBeInTheDocument();
    expect(within(option).getByText('Task, Bug')).toBeInTheDocument();
  });
});
