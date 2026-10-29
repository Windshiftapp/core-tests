import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ confirm: vi.fn() }));

vi.mock('../api/portal.js', () => ({
  contactRoles: {
        getAll: vi.fn(),
        create: vi.fn(),
        update: vi.fn(),
        delete: vi.fn(),
      },
}));

vi.mock('../stores/i18n.svelte.js', () => ({ t: (key) => key }));

vi.mock('../composables/useConfirm.js', () => ({ confirm: mocks.confirm }));

import { contactRoles } from '../api/portal.js';
import ContactLevelManager from './ContactLevelManager.svelte';

describe('ContactLevelManager (WI-1139)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    contactRoles.getAll.mockResolvedValue([]);
  });

  it('renders levels ordered by sort_order', async () => {
    contactRoles.getAll.mockResolvedValue([
      { id: 2, name: 'Billing', sort_order: 20 },
      { id: 1, name: 'Manager', sort_order: 10 },
    ]);

    render(ContactLevelManager);

    await screen.findByTestId('contact-level-row-1');
    const rows = screen.getAllByTestId(/contact-level-row-/);
    expect(rows.map((row) => row.getAttribute('data-testid'))).toEqual([
      'contact-level-row-1',
      'contact-level-row-2',
    ]);
  });

  it('creates a contact level', async () => {
    contactRoles.create.mockResolvedValue({});

    render(ContactLevelManager);

    const addButton = await screen.findByTestId('contact-level-add');
    await waitFor(() => expect(addButton).not.toBeDisabled());
    await fireEvent.click(addButton);
    await fireEvent.input(document.querySelector('#contact-level-name'), {
      target: { value: 'Manager' },
    });
    await fireEvent.click(screen.getByTestId('entity-form-confirm'));

    await waitFor(() => {
      expect(contactRoles.create).toHaveBeenCalledWith(
        expect.objectContaining({ name: 'Manager' })
      );
    });
  });

  it('deletes a level after confirmation', async () => {
    contactRoles.getAll.mockResolvedValue([{ id: 5, name: 'Billing', sort_order: 1 }]);
    contactRoles.delete.mockResolvedValue({});
    mocks.confirm.mockResolvedValue(true);

    render(ContactLevelManager);

    await fireEvent.click(await screen.findByTestId('contact-level-delete-5'));

    await waitFor(() => {
      expect(contactRoles.delete).toHaveBeenCalledWith(5);
    });
  });

  it('hides actions for system levels', async () => {
    contactRoles.getAll.mockResolvedValue([
      { id: 1, name: 'Portal Customer', sort_order: 0, is_system: true },
    ]);

    render(ContactLevelManager);

    await screen.findByTestId('contact-level-row-1');
    expect(screen.queryByTestId('contact-level-edit-1')).toBeNull();
    expect(screen.queryByTestId('contact-level-delete-1')).toBeNull();
  });
});
