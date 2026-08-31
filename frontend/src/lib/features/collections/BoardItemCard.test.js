import { cleanup, render, screen, within } from '@testing-library/svelte';
import { afterEach, describe, expect, test } from 'vitest';
import BoardItemCard from './BoardItemCard.svelte';

afterEach(() => {
  cleanup();
});

describe('BoardItemCard metadata footer', () => {
  function renderCard() {
    return render(BoardItemCard, {
      props: {
        item: {
          id: 42,
          workspace_key: 'WIND',
          workspace_item_number: 42,
          title: 'Footer-only metadata',
          item_type_id: 7,
        },
        workspace: { id: 1, key: 'WIND' },
        itemTypes: [{ id: 7, name: 'Story', icon: 'BookOpen', color: '#8b5cf6' }],
      },
    });
  }

  test('renders a one-line card without a footer divider', () => {
    renderCard();

    const footer = screen.getByTestId('board-card-footer-42');

    expect(footer).not.toHaveClass('border-t');
    expect(footer).not.toHaveAttribute('style');
  });

  test('always groups the colored item type icon and sans-serif key at the left', () => {
    renderCard();

    const footer = screen.getByTestId('board-card-footer-42');
    const leftMetadata = footer.firstElementChild;
    const typeIcon = screen.getByTestId('board-card-type-icon-42');
    const itemKey = within(footer).getByText('WIND-42');

    expect(leftMetadata).toContainElement(typeIcon);
    expect(leftMetadata).toContainElement(itemKey);
    expect(typeIcon).toHaveAttribute('title', 'Story');
    expect(typeIcon).toHaveStyle({ color: 'rgb(139, 92, 246)' });
    expect(typeIcon.style.backgroundColor).toBe('');
    expect(itemKey).not.toHaveClass('font-mono');
    expect(screen.queryByTitle('Item type')).not.toBeInTheDocument();
    expect(screen.queryByText('Story')).not.toBeInTheDocument();
  });
});

describe('BoardItemCard configured fields', () => {
  test('renders global labels returned on the item', () => {
    render(BoardItemCard, {
      props: {
        item: {
          id: 42,
          workspace_key: 'WIND',
          workspace_item_number: 42,
          title: 'Labelled item',
          labels: [{ id: 9, name: 'Customer', color: '#2563eb' }],
        },
        cardFields: [{ field_type: 'system', field_identifier: 'labels', display_order: 0 }],
      },
    });

    expect(screen.getByText('Customer')).toBeInTheDocument();
    expect(screen.getByTitle('Label')).toBeInTheDocument();
  });
});
