import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterAll, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  getCollections: vi.fn(),
  getWorkspaces: vi.fn(),
  getItems: vi.fn(),
  navigate: vi.fn(),
}));

vi.mock('../../api.js', () => ({
  api: {
    collections: { getAll: mocks.getCollections },
    workspaces: { getAll: mocks.getWorkspaces },
    items: { getAll: mocks.getItems },
  },
}));

vi.mock('../../router.js', () => ({ navigate: mocks.navigate }));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key, params = {}) =>
    ({
      'common.retry': 'Retry',
      'widgets.savedSearch.loadingCollections': 'Loading saved collections...',
      'widgets.savedSearch.setupTitle': 'Choose a saved collection',
      'widgets.savedSearch.setupSubtitle': 'Select a collection to show its work items here.',
      'widgets.savedSearch.selectCollection': 'Select a collection',
      'widgets.savedSearch.noCollections': 'No saved collections available',
      'widgets.savedSearch.collectionUnavailable': 'Saved collection unavailable',
      'widgets.savedSearch.itemCount': `${params.count} items`,
      'widgets.savedSearch.emptyTitle': 'No matching work items',
      'widgets.savedSearch.emptySubtitle': 'This saved collection has no matching items',
      'widgets.savedSearch.loadError': 'Failed to load saved collection',
      'collections.newCollection': 'New Collection',
      'pickers.select': 'Select',
      'pickers.noItemsFound': 'No items found',
    })[key] ?? key,
}));

import SavedSearchWidget from './SavedSearchWidget.svelte';

describe('SavedSearchWidget', () => {
  const originalAnimate = Element.prototype.animate;

  beforeEach(() => {
    Element.prototype.animate = vi.fn(() => ({
      cancel: vi.fn(),
      finished: Promise.resolve(),
    }));
    mocks.getCollections.mockResolvedValue([]);
    mocks.getWorkspaces.mockResolvedValue([]);
    mocks.getItems.mockResolvedValue({ items: [] });
  });

  afterAll(() => {
    if (originalAnimate) {
      Element.prototype.animate = originalAnimate;
    } else {
      delete Element.prototype.animate;
    }
  });

  it('loads the selected collection and renders dashboard item rows', async () => {
    mocks.getCollections.mockResolvedValue([{ id: 9, name: 'Release queue', workspace_id: 7 }]);
    mocks.getItems.mockResolvedValue({
      items: [
        {
          id: 21,
          title: 'Ship saved search widget',
          workspace_id: 7,
          workspace_key: 'CORE',
          workspace_item_number: 21,
          status_name: 'In Progress',
          status_color: '#579dff',
          priority_name: 'High',
          priority_color: '#e56910',
        },
      ],
    });

    render(SavedSearchWidget, { workspaceId: 7, config: { collectionId: 9 } });

    await waitFor(() => expect(screen.getByText('Ship saved search widget')).toBeInTheDocument());
    expect(mocks.getCollections).toHaveBeenCalledWith({ workspace_id: 7 });
    expect(mocks.getItems).toHaveBeenCalledWith({
      collection_id: '9',
      limit: 10,
      order_by: 'updated_at',
      sort_direction: 'desc',
      fields: 'summary',
    });
    expect(screen.getByTestId('dashboard-item-key')).toHaveTextContent('CORE-21');
    expect(screen.getByText('In Progress')).toBeInTheDocument();
  });

  it('offers scoped collections for configuration and persists the selected id', async () => {
    mocks.getCollections.mockResolvedValue([{ id: 12, name: 'Current sprint', workspace_id: 7 }]);
    const onconfigchange = vi.fn();

    render(SavedSearchWidget, { workspaceId: 7, config: {}, onconfigchange });

    const selector = await screen.findByRole('combobox', { name: 'Select a collection' });
    await fireEvent.click(selector);
    await fireEvent.click(await screen.findByRole('option', { name: 'Current sprint' }));

    expect(onconfigchange).toHaveBeenCalledWith({ collectionId: 12 });
  });

  it('filters a large collection list by typing in the combobox', async () => {
    mocks.getCollections.mockResolvedValue([
      ...Array.from({ length: 200 }, (_, index) => ({
        id: index + 1,
        name: `Archived collection ${index + 1}`,
        workspace_id: 7,
      })),
      { id: 301, name: 'Release queue', workspace_id: 7 },
    ]);
    const onconfigchange = vi.fn();

    render(SavedSearchWidget, { workspaceId: 7, config: {}, onconfigchange });

    const selector = await screen.findByRole('combobox', { name: 'Select a collection' });
    await fireEvent.click(selector);
    await fireEvent.input(selector, { target: { value: 'Release queue' } });

    expect(await screen.findByRole('option', { name: 'Release queue' })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: 'Archived collection 1' })).not.toBeInTheDocument();

    await fireEvent.keyDown(selector, { key: 'Enter' });
    expect(onconfigchange).toHaveBeenCalledWith({ collectionId: 301 });
  });

  it('offers collection creation when there is nothing to select', async () => {
    const createModalListener = vi.fn();
    window.addEventListener('show-create-modal', createModalListener, { once: true });

    render(SavedSearchWidget, { workspaceId: 7, config: {} });

    await fireEvent.click(await screen.findByTestId('saved-search-create-collection'));

    expect(createModalListener).toHaveBeenCalledOnce();
    expect(createModalListener.mock.calls[0][0].detail).toEqual({
      type: 'collection',
      workspaceId: 7,
    });
  });

  it('keeps the collection selector available after configuration', async () => {
    mocks.getCollections.mockResolvedValue([
      { id: 12, name: 'Current sprint', workspace_id: 7 },
      { id: 13, name: 'Release queue', workspace_id: 7 },
    ]);
    const onconfigchange = vi.fn();

    render(SavedSearchWidget, {
      workspaceId: 7,
      config: { collectionId: 12 },
      onconfigchange,
    });

    const selector = await screen.findByRole('combobox', { name: 'Select a collection' });
    await fireEvent.click(selector);
    await fireEvent.click(await screen.findByRole('option', { name: 'Release queue' }));

    expect(onconfigchange).toHaveBeenCalledWith({ collectionId: 13 });
  });
});
