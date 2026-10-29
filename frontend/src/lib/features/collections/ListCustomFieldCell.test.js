import { render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';

vi.mock('../../api/users.js', () => ({
  getUsers: vi.fn(),
}))

vi.mock('../../api/assets.js', () => ({
  assets: {
        getSummaries: vi.fn(async () => []),
        getAll: vi.fn(async () => ({ data: [], pagination: { total_items: 0 } })),
      },
}))

vi.mock('../../api/portal.js', () => ({
  portalCustomers: { getAll: vi.fn(async () => []) },
  customerOrganisations: { getAll: vi.fn(async () => []) },
}));

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
import { getUsers } from '../../api/users.js';
import { assets } from '../../api/assets.js';
import { customerOrganisations, portalCustomers } from '../../api/portal.js';



const editorOptionsStub = {
  users: [],
  loaded: {},
  loading: {},
};

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

describe('ListCustomFieldCell asset references (issue #276)', () => {
  it('renders the assigned asset label for editors instead of an empty cell', () => {
    render(ListCustomFieldCell, {
      props: {
        field: {
          id: 9,
          name: 'Machine',
          field_type: 'asset',
          options: JSON.stringify({ asset_set_id: 3 }),
        },
        value: { id: 5, asset_tag: 'A-005', title: 'Press' },
        canEdit: true,
        users: [],
        editorOptions: editorOptionsStub,
        workspaceId: 1,
        itemId: 77,
      },
    });

    const label = screen.getByText('A-005 - Press');
    expect(label.closest('button')).not.toBeNull();
    expect(screen.getByTestId('list-custom-field-9-77')).toBeInTheDocument();
  });

  it('falls back to the asset id label when the stored value is a bare id', () => {
    render(ListCustomFieldCell, {
      props: {
        field: {
          id: 9,
          name: 'Machine',
          field_type: 'asset',
          options: JSON.stringify({ asset_set_id: 3 }),
        },
        value: 5,
        canEdit: true,
        users: [],
        editorOptions: editorOptionsStub,
        workspaceId: 1,
        itemId: 77,
      },
    });

    // The lookup fails (empty summaries), but the cell still shows a label
    // rather than the placeholder an unresolved picker input produced.
    expect(screen.getByText('Asset #5')).toBeInTheDocument();
  });
});
