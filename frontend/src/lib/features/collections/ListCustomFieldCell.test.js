import { render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';

vi.mock('../../stores/collectionEditorOptions.svelte.js', () => ({
  collectionEditorOptions: {
    load: vi.fn(),
    loadAssets: vi.fn(),
  },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

import ListCustomFieldCell from './ListCustomFieldCell.svelte';

describe('ListCustomFieldCell user references', () => {
  it('uses page-level users for a readonly multi-user field before editor options load', () => {
    render(ListCustomFieldCell, {
      props: {
        field: { id: 15, name: 'Reviewers', field_type: 'multi_user' },
        value: [42, 7],
        canEdit: false,
        users: [
          { id: 42, first_name: 'Ada', last_name: 'Lovelace', username: 'ada' },
          { id: 7, first_name: 'Grace', last_name: 'Hopper', username: 'grace' },
        ],
        editorOptions: {
          users: [],
          loaded: {},
          loading: {},
        },
      },
    });

    expect(screen.getByText('Ada Lovelace, Grace Hopper')).toBeInTheDocument();
    expect(screen.queryByText('#42, #7')).not.toBeInTheDocument();
  });
});
