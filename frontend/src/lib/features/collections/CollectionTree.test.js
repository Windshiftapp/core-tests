import { cleanup, fireEvent, render, screen, within } from '@testing-library/svelte';
import { tick } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

const { getManyAncestors } = vi.hoisted(() => ({
  getManyAncestors: vi.fn(),
}));

vi.mock('../../stores/collectionContext.js', async () => {
  const { collectionTreeStore } = await import('./collectionTreeStoreMock.svelte.js');
  return { collectionStore: collectionTreeStore, scheduleCollectionReconciliation: vi.fn() };
});
vi.mock('../../stores/auth.svelte.js', () => ({
  authStore: { currentUser: { id: 1 } },
}))

vi.mock('../../stores/itemTestCaseLinksStore.svelte.js', () => ({
  itemTestCaseLinksStore: {
      initialize: vi.fn(),
      loadForItems: vi.fn().mockResolvedValue(undefined),
      get: vi.fn().mockReturnValue(null),
    },
}))

vi.mock('../../stores/workspaceDataStore.svelte.js', () => ({
  workspaceDataStore: {
      workspace: { id: 1, name: 'Windshift', key: 'WS' },
      itemTypes: [],
      statuses: [],
      statusCategories: [],
      priorities: [],
      users: [],
      milestones: [],
      iterations: [],
      projects: [],
      customFieldDefinitions: [],
      initialize: vi.fn().mockResolvedValue(undefined),
      initializeGlobal: vi.fn().mockResolvedValue(undefined),
    },
}));
vi.mock('../../api/items.js', () => ({
  items: { getManyAncestors },
}));
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
      'layout.items': 'items',
      'collections.itemsShown': '{count} shown',
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
import { items } from '../../api/items.js';

function item(id, parentId, title = `Item ${id}`) {
  return {
    id,
    parent_id: parentId,
    title,
    workspace_item_number: id,
    status_id: 1,
    created_at: '2026-01-01T00:00:00Z',
  };
}

function deferred() {
  let resolve;
  const promise = new Promise((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

function rowIndent(testId) {
  const id = testId.replace('tree-row-', '');
  return screen.getByTestId(`tree-indent-${id}`).style.paddingLeft;
}

afterEach(() => {
  cleanup();
  localStorage.clear();
  collectionTreeStore.items = [];
  collectionTreeStore.itemsPagination = null;
  collectionTreeStore.collectionTotal = null;
  collectionTreeStore.itemsHasMore = false;
  collectionTreeStore.itemsLoadingMore = false;
  collectionTreeStore.itemsRemainingCount = 0;
  vi.clearAllMocks();
});

describe('CollectionTree ancestor context', () => {
  it('nests orphaned children under a fetched parent instead of leaving them as roots', async () => {
    const parentChain = deferred();
    getManyAncestors.mockReturnValue(parentChain.promise);
    collectionTreeStore.items = [item(1, null), item(20, 13)];

    render(CollectionTree, { workspaceId: 1 });

    expect(await screen.findByTestId('tree-row-20')).toBeInTheDocument();
    expect(screen.queryByTestId('tree-row-13')).not.toBeInTheDocument();
    expect(rowIndent('tree-row-20')).toBe('0rem');

    parentChain.resolve([{ item_id: 20, ancestors: [item(13, null, 'BizNut racing game')] }]);
    expect(await screen.findByTestId('tree-row-13')).toBeInTheDocument();

    const parentRow = screen.getByTestId('tree-row-13').closest('.tree-row');
    const childRow = screen.getByTestId('tree-row-20').closest('.tree-row');
    expect(
      parentRow.compareDocumentPosition(childRow) & Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
    expect(rowIndent('tree-row-13')).toBe('0rem');
    expect(rowIndent('tree-row-20')).toBe('1.5rem');
    // The new parent is expanded so the child stays visible.
    expect(within(parentRow).getByRole('button', { name: 'Collapse' })).toBeInTheDocument();
    expect(getManyAncestors).toHaveBeenCalledTimes(1);
    expect(getManyAncestors).toHaveBeenCalledWith([20]);
  });

  it('resolves multiple missing parents with a single batched request', async () => {
    getManyAncestors.mockResolvedValue([
      { item_id: 20, ancestors: [item(13, null, 'First parent')] },
      { item_id: 30, ancestors: [item(40, null, 'Second parent')] },
    ]);
    collectionTreeStore.items = [item(20, 13), item(21, 13), item(30, 40)];

    render(CollectionTree, { workspaceId: 1 });

    expect(await screen.findByTestId('tree-row-13')).toBeInTheDocument();
    expect(await screen.findByTestId('tree-row-40')).toBeInTheDocument();
    expect(getManyAncestors).toHaveBeenCalledTimes(1);
    // One representative orphan per missing parent: siblings share a chain.
    expect(getManyAncestors).toHaveBeenCalledWith([20, 30]);
    expect(rowIndent('tree-row-21')).toBe('1.5rem');
  });

  it('keeps a multi-level chain nested from root to leaf', async () => {
    getManyAncestors.mockResolvedValue([
      {
        item_id: 30,
        ancestors: [item(5, null, 'Grandparent'), item(12, 5, 'Middle'), item(20, 12, 'Parent')],
      },
    ]);
    collectionTreeStore.items = [item(30, 20)];

    render(CollectionTree, { workspaceId: 1 });

    expect(await screen.findByTestId('tree-row-30')).toBeInTheDocument();
    expect(rowIndent('tree-row-5')).toBe('0rem');
    expect(rowIndent('tree-row-12')).toBe('1.5rem');
    expect(rowIndent('tree-row-20')).toBe('3rem');
    expect(rowIndent('tree-row-30')).toBe('4.5rem');
  });

  it('covers a page of many distinct missing parents with one batched request', async () => {
    getManyAncestors.mockImplementation((ids) =>
      Promise.resolve(
        ids.map((orphanId) => ({
          item_id: orphanId,
          ancestors: [item(orphanId + 1000, null, `Parent ${orphanId}`)],
        })),
      ),
    );
    collectionTreeStore.items = Array.from({ length: 12 }, (_, index) =>
      item(100 + index, 1100 + index),
    );

    render(CollectionTree, { workspaceId: 1 });

    expect(await screen.findByTestId('tree-row-1111')).toBeInTheDocument();
    expect(getManyAncestors).toHaveBeenCalledTimes(1);
    expect(getManyAncestors).toHaveBeenCalledWith([
      100, 101, 102, 103, 104, 105, 106, 107, 108, 109, 110, 111,
    ]);
    for (let id = 100; id < 112; id++) {
      expect(screen.getByTestId(`tree-row-${id + 1000}`)).toBeInTheDocument();
    }
  });

  it('does not fetch ancestors when every parent is already loaded', async () => {
    getManyAncestors.mockResolvedValue([]);
    collectionTreeStore.items = [item(13, null), item(20, 13)];

    render(CollectionTree, { workspaceId: 1 });

    expect(await screen.findByTestId('tree-row-20')).toBeInTheDocument();
    expect(rowIndent('tree-row-20')).toBe('1.5rem');
    expect(getManyAncestors).not.toHaveBeenCalled();
  });

  it('keeps the hierarchy intact when a reload loads the parent itself', async () => {
    getManyAncestors.mockResolvedValue([
      { item_id: 20, ancestors: [item(13, null, 'BizNut racing game')] },
    ]);
    collectionTreeStore.items = [item(20, 13)];

    render(CollectionTree, { workspaceId: 1 });
    expect(await screen.findByTestId('tree-row-13')).toBeInTheDocument();

    // Simulate toggling the completed filter: the reload now includes the
    // parent, so the fetched context must be dropped without a refetch.
    collectionTreeStore.items = [item(13, null), item(20, 13)];
    await tick();

    expect(rowIndent('tree-row-13')).toBe('0rem');
    expect(rowIndent('tree-row-20')).toBe('1.5rem');
    expect(getManyAncestors).toHaveBeenCalledTimes(1);
  });

  it('shows the filter-scoped item total in the header, not the rendered rows', async () => {
    getManyAncestors.mockResolvedValue([]);
    collectionTreeStore.items = [item(1, null), item(20, 13)];
    collectionTreeStore.collectionTotal = 1310;
    collectionTreeStore.itemsPagination = { total_items: 803, page: 1 };

    render(CollectionTree, { workspaceId: 1 });

    expect(await screen.findByTestId('page-header-subtitle')).toHaveTextContent(
      '1310 items · 803 shown',
    );
  });

  it('omits the shown segment when no item is filtered out', async () => {
    getManyAncestors.mockResolvedValue([]);
    collectionTreeStore.items = [item(1, null), item(2, null)];
    collectionTreeStore.collectionTotal = 2;
    collectionTreeStore.itemsPagination = { total_items: 2, page: 1 };

    render(CollectionTree, { workspaceId: 1 });

    expect(await screen.findByTestId('page-header-subtitle')).toHaveTextContent('2 items');
    expect(screen.getByTestId('page-header-subtitle')).not.toHaveTextContent('shown');
  });

  it('renders every loaded root instead of a client page of them', async () => {
    getManyAncestors.mockResolvedValue([]);
    collectionTreeStore.items = Array.from({ length: 60 }, (_, index) => item(index + 1, null));
    collectionTreeStore.collectionTotal = 60;
    collectionTreeStore.itemsPagination = { total_items: 60, page: 1 };

    render(CollectionTree, { workspaceId: 1 });

    expect(await screen.findByTestId('tree-row-60')).toBeInTheDocument();
    expect(screen.queryByText(/Top-level items/)).not.toBeInTheDocument();
  });

  it('offers a server continuation when more items remain', async () => {
    getManyAncestors.mockResolvedValue([]);
    collectionTreeStore.items = [item(1, null)];
    collectionTreeStore.itemsPagination = { total_items: 300, page: 1 };
    collectionTreeStore.itemsHasMore = true;
    collectionTreeStore.itemsRemainingCount = 200;

    render(CollectionTree, { workspaceId: 1 });

    const button = await screen.findByTestId('tree-load-more-button');
    expect(button).toHaveTextContent('Load more');
    expect(button).toHaveTextContent('200 remaining');

    await fireEvent.click(button);
    expect(collectionTreeStore.loadMoreItems).toHaveBeenCalledTimes(1);
  });
});
