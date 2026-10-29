import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

const { store } = vi.hoisted(() => ({
  store: {
    itemsHasMore: false,
    itemsLoadingMore: false,
    itemsRemainingCount: 0,
    loadMoreItems: vi.fn(),
  },
}));

vi.mock('../../stores/collectionContext.js', () => ({ collectionStore: store }));
vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) =>
    ({
      'common.loadMore': 'Load more',
      'common.loading': 'Loading',
      'common.remaining': 'remaining',
    })[key] ?? key,
}));

import CollectionHierarchyLoadMore from './CollectionHierarchyLoadMore.svelte';

afterEach(() => {
  cleanup();
  store.itemsHasMore = false;
  store.itemsLoadingMore = false;
  store.itemsRemainingCount = 0;
  store.loadMoreItems.mockReset();
});

describe('CollectionHierarchyLoadMore', () => {
  it('renders nothing once every page is loaded', () => {
    render(CollectionHierarchyLoadMore, { testid: 'map-load-more' });

    expect(screen.queryByTestId('map-load-more')).not.toBeInTheDocument();
  });

  it('offers a continuation control with the remaining count', async () => {
    store.itemsHasMore = true;
    store.itemsRemainingCount = 750;
    render(CollectionHierarchyLoadMore, { testid: 'map-load-more' });

    const button = screen.getByTestId('map-load-more-button');
    expect(button).toHaveTextContent('Load more');
    expect(button).toHaveTextContent('750 remaining');

    await fireEvent.click(button);
    expect(store.loadMoreItems).toHaveBeenCalledTimes(1);
  });

  it('disables the control while the next page is loading', () => {
    store.itemsHasMore = true;
    store.itemsLoadingMore = true;
    render(CollectionHierarchyLoadMore, { testid: 'roadmap-load-more' });

    const button = screen.getByTestId('roadmap-load-more-button');
    expect(button).toBeDisabled();
    expect(button).toHaveTextContent('Loading');
  });
});
