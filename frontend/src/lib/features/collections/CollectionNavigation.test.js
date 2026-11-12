import { cleanup, render, screen } from '@testing-library/svelte';
import { writable } from 'svelte/store';
import { afterEach, describe, expect, it, vi } from 'vitest';

const { collectionStore } = vi.hoisted(() => ({
  collectionStore: {
    boardCollection: { name: 'All of mine', created_by: 1 },
    collectionName: 'All of mine',
    itemsPagination: { total_items: 293 },
    collectionTotal: 671,
  },
}));

const mocks = vi.hoisted(() => ({
  enabledViewIds: vi.fn(() => ['backlog', 'board', 'list', 'tree', 'map', 'roadmap']),
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => (key === 'layout.items' ? 'items' : 'Collection'),
}));

vi.mock('../../stores/collectionContext.js', () => ({ collectionStore }));
vi.mock('../../stores/viewSettings.svelte.js', () => ({
  viewSettingsStore: {
    load: vi.fn().mockResolvedValue(undefined),
    enabledViewIds: mocks.enabledViewIds,
  },
}));
vi.mock('../../stores/auth.svelte.js', () => ({
  authStore: { currentUser: { id: 1 } },
}));
vi.mock('../../router.js', () => ({
  navigate: vi.fn(),
  currentRoute: writable({ view: 'collection-board' }),
}));
vi.mock('../../stores/ui.svelte.js', () => ({
  uiStore: writable({ wsSidebarWidth: 260, wsSidebarCollapsed: false }),
  WS_SIDEBAR_DEFAULT_WIDTH: 260,
}));

import CollectionNavigation from './CollectionNavigation.svelte';

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  mocks.enabledViewIds.mockReturnValue(['backlog', 'board', 'list', 'tree', 'map', 'roadmap']);
  collectionStore.boardCollection = { name: 'All of mine', created_by: 1 };
});

describe('collection navigation item count', () => {
  it.each([
    ['including separately loaded board items', 293, 671],
    ['without separately loaded items', 293, 293],
    ['with an empty collection', 0, 0],
    ['with an unavailable total', 293, null],
  ])('shows the collection total %s', (_scenario, mainTotal, total) => {
    collectionStore.itemsPagination = { total_items: mainTotal };
    collectionStore.collectionTotal = total;

    render(CollectionNavigation, { collectionId: '7' });

    expect(screen.getByTestId('collection-sidebar-count')).toHaveTextContent(
      total === null ? 'Collection' : `Collection · ${total} items`
    );
  });
});

describe('collection navigation visibility', () => {
  it('renders only the views enabled for the collection scope', () => {
    mocks.enabledViewIds.mockReturnValue(['board', 'list']);

    render(CollectionNavigation, { collectionId: '7' });

    expect(screen.getByTestId('collection-nav-board')).toBeInTheDocument();
    expect(screen.getByTestId('collection-nav-list')).toBeInTheDocument();
    expect(screen.queryByTestId('collection-nav-backlog')).not.toBeInTheDocument();
    expect(screen.queryByTestId('collection-nav-tree')).not.toBeInTheDocument();
  });

  it('offers the navigation-config link to the collection creator', () => {
    render(CollectionNavigation, { collectionId: '7' });

    expect(screen.getByTestId('collection-nav-config-link')).toBeInTheDocument();
  });

  it('hides the navigation-config link from non-creators', () => {
    collectionStore.boardCollection = { name: 'All of mine', created_by: 99 };

    render(CollectionNavigation, { collectionId: '7' });

    expect(screen.queryByTestId('collection-nav-config-link')).not.toBeInTheDocument();
  });
});
