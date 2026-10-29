import { Pencil, Trash2 } from '@lucide/svelte';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

import EntityRowActions from './EntityRowActions.svelte';

afterEach(cleanup);

describe('EntityRowActions', () => {
  it('renders configured actions and forwards their callbacks', async () => {
    const onEdit = vi.fn();
    render(EntityRowActions, {
      actions: [
        { id: 'edit', icon: Pencil, title: 'Edit', testId: 'edit-action', onclick: onEdit },
        { id: 'delete', icon: Trash2, title: 'Delete', testId: 'delete-action', danger: true },
      ],
    });

    await fireEvent.click(screen.getByTestId('edit-action'));
    expect(onEdit).toHaveBeenCalledOnce();
  });
});
