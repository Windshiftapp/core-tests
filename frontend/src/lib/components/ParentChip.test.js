import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, test, vi } from 'vitest';

vi.mock('../../stores/i18n.svelte.js', () => ({ t: (key) => key, i18n: { locale: 'en' } }));

import ParentChip from './ParentChip.svelte';

afterEach(cleanup);

describe('ParentChip', () => {
  test('renders the permission-checked parent key and title', () => {
    render(ParentChip, {
      props: {
        item: {
          id: 42,
          parent_id: 7,
          parent_key: 'EPIC-7',
          parent_title: 'Release the new workflow',
          workspace_key: 'CHILD',
        },
      },
    });

    const chip = screen.getByTestId('work-item-parent-42');
    expect(chip).toHaveTextContent('EPIC-7');
    expect(chip).toHaveTextContent('Release the new workflow');
    // The row must not derive a key from the child's own workspace.
    expect(chip).not.toHaveTextContent('CHILD-7');
  });

  test('derives the key when the summary omits parent_key', () => {
    render(ParentChip, {
      props: {
        item: {
          id: 42,
          parent_id: 7,
          parent_workspace_item_number: 7,
          parent_title: 'Release the new workflow',
          workspace_key: 'CHILD',
        },
      },
    });

    expect(screen.getByTestId('work-item-parent-42')).toHaveTextContent('CHILD-7');
  });

  test('renders nothing for a masked parent', () => {
    render(ParentChip, {
      props: {
        item: {
          id: 42,
          parent_id: 7,
          parent_key: '',
          parent_title: '',
          parent_workspace_item_number: null,
          workspace_key: 'CHILD',
        },
      },
    });

    expect(screen.queryByTestId('work-item-parent-42')).not.toBeInTheDocument();
  });

  test('honours a custom container width', () => {
    render(ParentChip, {
      props: { item: { id: 42, parent_id: 7, parent_key: 'EPIC-7' }, maxWidth: '12rem' },
    });

    expect(screen.getByTestId('work-item-parent-container-42')).toHaveStyle({ maxWidth: '12rem' });
  });
});
