import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api.js', () => ({
  api: {
    links: {
      create: vi.fn(),
      delete: vi.fn(),
      getFieldLinks: vi.fn(),
      search: vi.fn(),
    },
  },
}));

vi.mock('../stores/toasts.svelte.js', () => ({
  errorToast: vi.fn(),
}));

import { api } from '../api.js';
import LinkingFieldPicker from './LinkingFieldPicker.svelte';

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('LinkingFieldPicker item links', () => {
  it('routes an outgoing linked item with its numeric workspace id', () => {
    render(LinkingFieldPicker, {
      props: {
        fieldId: 8,
        itemId: 101,
        readonly: true,
        links: [
          {
            id: 1,
            target_type: 'item',
            target_id: 202,
            target_title: 'Cross-workspace target',
            target_workspace_key: 'OTHER',
            target_workspace_id: 77,
          },
        ],
      },
    });

    expect(screen.getByRole('link', { name: 'Cross-workspace target' })).toHaveAttribute(
      'href',
      '/workspaces/77/items/202'
    );
  });

  it('routes a mirror-field source with its numeric workspace id', () => {
    render(LinkingFieldPicker, {
      props: {
        fieldId: 9,
        itemId: 303,
        fieldOptions: JSON.stringify({ mirror_of_field_id: 8 }),
        readonly: true,
        links: [
          {
            id: 2,
            source_type: 'item',
            source_id: 404,
            source_title: 'Mirror source',
            source_workspace_key: 'SOURCE',
            source_workspace_id: 88,
          },
        ],
      },
    });

    expect(screen.getByRole('link', { name: 'Mirror source' })).toHaveAttribute(
      'href',
      '/workspaces/88/items/404'
    );
  });

  it('reports both item rows affected by removing a link', async () => {
    const onChanged = vi.fn();
    render(LinkingFieldPicker, {
      props: {
        fieldId: 8,
        itemId: 101,
        onChanged,
        links: [
          {
            id: 3,
            source_type: 'item',
            source_id: 101,
            target_type: 'item',
            target_id: 202,
            target_title: 'Linked row',
            target_workspace_id: 77,
          },
        ],
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: 'Remove link' }));

    await waitFor(() => {
      expect(onChanged).toHaveBeenCalledWith({ itemIds: [101, 202] });
    });
  });

  it('lets the server perform the one required source-target swap for mirror fields', async () => {
    const onChanged = vi.fn();
    api.links.search.mockResolvedValueOnce([
      { id: 404, title: 'Mirror source candidate', type: 'item' },
    ]);
    render(LinkingFieldPicker, {
      props: {
        fieldId: 9,
        itemId: 303,
        fieldOptions: JSON.stringify({
          link_type_id: 5,
          mirror_of_field_id: 8,
          allowed_entity_types: ['item'],
        }),
        links: [],
        onChanged,
      },
    });

    await fireEvent.click(screen.getByRole('button', { name: 'Add' }));
    await fireEvent.input(screen.getByPlaceholderText('Search items...'), {
      target: { value: 'Mirror source' },
    });
    await waitFor(() => expect(api.links.search).toHaveBeenCalled());
    await fireEvent.click(screen.getByRole('button', { name: 'Mirror source candidate' }));

    expect(api.links.create).toHaveBeenCalledWith({
      link_type_id: 5,
      source_type: 'item',
      source_id: 303,
      target_type: 'item',
      target_id: 404,
      custom_field_id: 9,
    });
    expect(onChanged).toHaveBeenCalledWith({ itemIds: [303, 404] });
  });
});
