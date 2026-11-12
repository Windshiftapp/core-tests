import { cleanup, render, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

const { getHierarchy, preferencesState } = vi.hoisted(() => ({
  getHierarchy: vi.fn(),
  preferencesState: { viewState: {} },
}));

vi.mock('../stores/collectionContext.js', async () => {
  const { hierarchyStore } = await import('./collectionHierarchyStoreMock.svelte.js');
  return { collectionStore: hierarchyStore };
});

vi.mock('../api/items.js', () => ({ items: { getHierarchy } }));

vi.mock('../stores/collectionPreferences.svelte.js', () => {
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

import CollectionHierarchyHarness from './CollectionHierarchyHarness.svelte';
import { hierarchyStore } from './collectionHierarchyStoreMock.svelte.js';

function item(id, parentId, extra = {}) {
  return { id, parent_id: parentId, title: `Item ${id}`, frac_index: String(id), ...extra };
}

function group(rootId, items, { total = items.length, hasMore = false } = {}) {
  return { root_id: rootId, total, has_more: hasMore, items };
}

async function renderHarness(props = {}) {
  let hierarchy;
  render(CollectionHierarchyHarness, {
    props: {
      onready: (next) => {
        hierarchy = next;
      },
      ...props,
    },
  });
  await waitFor(() => expect(hierarchy).toBeDefined());
  return hierarchy;
}

afterEach(() => {
  cleanup();
  hierarchyStore.items = [];
  getHierarchy.mockReset();
  preferencesState.viewState = {};
});

describe('useCollectionHierarchy', () => {
  it('eager-loads base subtrees and nests descendants by parent', async () => {
    getHierarchy.mockResolvedValue([
      group(1, [
        item(3, 1, { depth: 1, has_children: true }),
        item(4, 1, { depth: 1 }),
        item(5, 3, { depth: 2 }),
      ]),
    ]);
    hierarchyStore.items = [item(1, null, { has_children: true }), item(2, null)];

    const hierarchy = await renderHarness();

    await waitFor(() => expect(hierarchy.getChildren(1)).toHaveLength(2));
    expect(hierarchy.allItems.map((entry) => entry.id).sort((a, b) => a - b)).toEqual([
      1, 2, 3, 4, 5,
    ]);
    expect(hierarchy.getChildren(1).map((entry) => entry.id)).toEqual([3, 4]);
    expect(hierarchy.getChildren(3).map((entry) => entry.id)).toEqual([5]);
    expect(hierarchy.hasChildren(3)).toBe(true);
    expect(hierarchy.hasChildren(4)).toBe(false);
    // Only the root that reported has_children is fetched.
    expect(getHierarchy).toHaveBeenCalledTimes(1);
    expect(getHierarchy.mock.calls[0][0].rootIds).toEqual([1]);
  });

  it('flattens a matching descendant whose intermediate parent is filtered out', async () => {
    getHierarchy.mockResolvedValue([
      group(1, [item(9, 8, { depth: 2 })]),
    ]);
    hierarchyStore.items = [item(1, null, { has_children: true })];

    const hierarchy = await renderHarness();

    await waitFor(() => expect(hierarchy.getChildren(1)).toHaveLength(1));
    expect(hierarchy.getChildren(1).map((entry) => entry.id)).toEqual([9]);
    expect(hierarchy.allItems.some((entry) => entry.id === 8)).toBe(false);
  });

  it('pages within a root with per-root offset for Load more', async () => {
    getHierarchy.mockImplementation(({ perRootOffset }) =>
      Promise.resolve([
        perRootOffset === 0
          ? group(1, [item(3, 1, { depth: 1 }), item(4, 1, { depth: 1 })], {
              total: 4,
              hasMore: true,
            })
          : group(1, [item(5, 1, { depth: 1 }), item(6, 1, { depth: 1 })], {
              total: 4,
              hasMore: false,
            }),
      ])
    );
    hierarchyStore.items = [item(1, null, { has_children: true })];

    const hierarchy = await renderHarness();
    await waitFor(() => expect(hierarchy.hasMore(1)).toBe(true));
    expect(hierarchy.remaining(1)).toBe(2);

    await hierarchy.loadMore(1);

    await waitFor(() => expect(hierarchy.getChildren(1)).toHaveLength(4));
    expect(hierarchy.hasMore(1)).toBe(false);
    expect(getHierarchy).toHaveBeenLastCalledWith(
      expect.objectContaining({ perRootOffset: 2 })
    );
  });

  it('negative-caches a loaded-but-empty subtree so the expander disappears', async () => {
    getHierarchy.mockResolvedValue([group(1, [])]);
    hierarchyStore.items = [item(1, null, { has_children: true })];

    const hierarchy = await renderHarness();

    await waitFor(() => expect(hierarchy.isLoaded(1)).toBe(true));
    expect(hierarchy.hasChildren(1)).toBe(false);
  });

  it('re-fetches loaded subtrees when a store refresh keeps the base rows', async () => {
    getHierarchy.mockResolvedValue([group(1, [item(3, 1, { depth: 1 })])]);
    hierarchyStore.items = [item(1, null, { has_children: true })];

    const hierarchy = await renderHarness();
    await waitFor(() => expect(hierarchy.getChildren(1)).toHaveLength(1));

    // Same base ids, new array plus a generation bump: a descendant was created.
    getHierarchy.mockResolvedValue([
      group(1, [item(3, 1, { depth: 1 }), item(4, 1, { depth: 1 })]),
    ]);
    hierarchyStore.items = [item(1, null, { has_children: true })];
    hierarchyStore.dataGeneration += 1;

    await waitFor(() => expect(hierarchy.getChildren(1)).toHaveLength(2));
  });

  it('expands a base root whose has_children only arrives after the level resolves', async () => {
    getHierarchy.mockResolvedValue([group(1, [item(3, 1, { depth: 1 })])]);
    // The first base page is unfiltered, so the row does not yet carry
    // has_children and the default expansion skips it.
    hierarchyStore.items = [item(1, null)];

    const hierarchy = await renderHarness();
    await waitFor(() => expect(hierarchy.isLoaded(1)).toBe(false));
    expect(hierarchy.isExpanded(1)).toBe(false);

    // The level resolves: the same base row now reports children without an id
    // change, so the default expansion must still apply.
    hierarchyStore.items = [item(1, null, { has_children: true })];
    hierarchyStore.dataGeneration += 1;

    await waitFor(() => expect(hierarchy.getChildren(1)).toHaveLength(1));
    expect(hierarchy.isExpanded(1)).toBe(true);
  });

  it('persists the default root expansion for the view', async () => {
    getHierarchy.mockResolvedValue([group(1, [item(3, 1, { depth: 1, has_children: true })])]);
    hierarchyStore.items = [item(1, null, { has_children: true })];

    const hierarchy = await renderHarness({ view: 'tree', workspaceId: 1 });
    await waitFor(() => expect(hierarchy.getChildren(1)).toHaveLength(1));

    expect(hierarchy.isExpanded(1)).toBe(true);
    expect(preferencesState.viewState['workspace-1'].tree.expanded_ids).toEqual([1]);

    hierarchy.collapse(1);
    expect(preferencesState.viewState['workspace-1'].tree.expanded_ids).toEqual([]);
  });

  it('restores a stored collapse instead of the default expansion', async () => {
    getHierarchy.mockResolvedValue([group(1, [item(3, 1, { depth: 1 })])]);
    preferencesState.viewState = { 'workspace-1': { tree: { expanded_ids: [] } } };
    hierarchyStore.items = [item(1, null, { has_children: true })];

    const hierarchy = await renderHarness({ view: 'tree', workspaceId: 1 });
    await waitFor(() => expect(hierarchy.isLoaded(1)).toBe(true));

    expect(hierarchy.isExpanded(1)).toBe(false);
    expect(hierarchy.getChildren(1)).toHaveLength(1);
  });

  it('does not persist expansion when the view has no persistence key', async () => {
    getHierarchy.mockResolvedValue([group(1, [item(3, 1, { depth: 1 })])]);
    hierarchyStore.items = [item(1, null, { has_children: true })];

    const hierarchy = await renderHarness();
    await waitFor(() => expect(hierarchy.getChildren(1)).toHaveLength(1));
    hierarchy.collapse(1);

    expect(preferencesState.viewState).toEqual({});
  });

  it('resets the overlay when the base set is replaced', async () => {
    getHierarchy.mockResolvedValue([group(1, [item(3, 1, { depth: 1 })])]);
    hierarchyStore.items = [item(1, null, { has_children: true })];

    const hierarchy = await renderHarness();
    await waitFor(() => expect(hierarchy.getChildren(1)).toHaveLength(1));

    getHierarchy.mockResolvedValue([group(10, [item(11, 10, { depth: 1 })])]);
    hierarchyStore.items = [item(10, null, { has_children: true })];
    await waitFor(() => expect(hierarchy.getChildren(10)).toHaveLength(1));

    expect(hierarchy.allItems.some((entry) => entry.id === 3)).toBe(false);
    expect(hierarchy.hasChildren(1)).toBe(false);
  });

  it('keeps the base page order for roots instead of re-sorting', async () => {
    // The API already returns roots in rank order; the loader must not
    // re-order them even when ids and rank disagree.
    hierarchyStore.items = [item(2, null), item(1, null), item(3, null)];

    const hierarchy = await renderHarness();

    expect(hierarchy.roots.map((entry) => entry.id)).toEqual([2, 1, 3]);
  });

  it('drops a subtree response for a base that was replaced', async () => {
    let resolveFirstFetch;
    getHierarchy.mockImplementationOnce(
      () => new Promise((resolve) => {
        resolveFirstFetch = resolve;
      })
    );
    hierarchyStore.items = [item(1, null, { has_children: true })];

    const hierarchy = await renderHarness();
    await waitFor(() => expect(getHierarchy).toHaveBeenCalledTimes(1));

    // Replace the base before the first subtree fetch resolves.
    getHierarchy.mockResolvedValue([group(10, [item(11, 10, { depth: 1 })])]);
    hierarchyStore.items = [item(10, null, { has_children: true })];
    await waitFor(() => expect(hierarchy.getChildren(10)).toHaveLength(1));

    // The stale response must not be merged into the current hierarchy.
    resolveFirstFetch([group(1, [item(3, 1, { depth: 1 })])]);
    await waitFor(() => expect(hierarchy.allItems.some((entry) => entry.id === 3)).toBe(false));
    expect(hierarchy.roots.map((entry) => entry.id)).toEqual([10]);
  });
});
