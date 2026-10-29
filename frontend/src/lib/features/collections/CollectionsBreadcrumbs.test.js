import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

import CollectionsBreadcrumbs from './CollectionsBreadcrumbs.svelte';

describe('CollectionsBreadcrumbs category selector', () => {
  it('reports the selected category id and clearing as null', async () => {
    const oncategorychange = vi.fn();
    render(CollectionsBreadcrumbs, {
      props: {
        isEditing: true,
        collection: { id: 1, name: 'Global collection', ql_query: 'status_id = 1', category_id: null },
        categories: [
          { id: 5, name: 'Support' },
          { id: 9, name: 'Operations' },
        ],
        oncategorychange,
      },
    });

    const trigger = screen.getByRole('combobox');
    await fireEvent.click(trigger);
    await fireEvent.click(await screen.findByText('Support'));
    expect(oncategorychange).toHaveBeenCalledWith(5);

    await fireEvent.click(trigger);
    await fireEvent.click(await screen.findByText('collections.noCategory'));
    expect(oncategorychange).toHaveBeenLastCalledWith(null);
  });

  it('hides owner-only controls from shared readers', () => {
    render(CollectionsBreadcrumbs, {
      props: {
        isEditing: true,
        canEdit: false,
        collection: { id: 1, name: 'Shared collection', ql_query: 'status_id = 1', created_by: 2 },
        categories: [{ id: 5, name: 'Support' }],
      },
    });

    expect(screen.getByText('Shared collection')).toBeTruthy();
    expect(screen.queryByTestId('collection-name')).toBeNull();
    expect(screen.queryByTestId('collection-save')).toBeNull();
    expect(screen.queryByTestId('collection-privacy-toggle')).toBeNull();
    expect(screen.queryByTestId('public-board-button')).toBeNull();
    expect(screen.queryByRole('combobox')).toBeNull();
  });
});
