import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api/items.js', () => ({
  items: {
        listParticipants: vi.fn(),
        addParticipant: vi.fn(),
        removeParticipant: vi.fn(),
      },
}))

vi.mock('../../api/portal.js', () => ({
  portalCustomers: { getAll: vi.fn().mockResolvedValue([]) },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('../../stores/toasts.svelte.js', () => ({
  errorToast: vi.fn(),
}));

import { portalCustomers } from '../../api/portal.js';
import { items } from '../../api/items.js';
import ParticipantsSection from './ParticipantsSection.svelte';

const participant = {
  id: 1,
  item_id: 42,
  portal_customer_id: 7,
  customer_name: 'Alice External',
  customer_email: 'alice@example.test',
};

describe('ParticipantsSection', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    portalCustomers.getAll.mockResolvedValue([]);
  });

  it('renders existing participants and the empty state', async () => {
    items.listParticipants.mockResolvedValue([participant]);
    render(ParticipantsSection, { itemId: 42 });

    expect(await screen.findByTestId('item-participant-7')).toBeTruthy();
    expect(screen.getByText('Alice External')).toBeTruthy();
    expect(screen.getByText('alice@example.test')).toBeTruthy();
    expect(screen.queryByText('items.participantsEmpty')).toBeNull();
  });

  it('shows the empty state when there are no participants', async () => {
    items.listParticipants.mockResolvedValue([]);
    render(ParticipantsSection, { itemId: 42 });

    await waitFor(() => expect(items.listParticipants).toHaveBeenCalledWith(42));
    expect(screen.getByText('items.participantsEmpty')).toBeTruthy();
  });

  it('keeps the add form hidden until the add toggle is used', async () => {
    items.listParticipants.mockResolvedValue([]);
    render(ParticipantsSection, { itemId: 42 });

    await screen.findByTestId('item-participants');
    expect(screen.queryByTestId('item-participant-email')).toBeNull();

    await fireEvent.click(screen.getByTestId('item-participant-add-toggle'));
    expect(screen.getByTestId('item-participant-email')).toBeTruthy();
    expect(screen.getByTestId('item-participant-name')).toBeTruthy();
    expect(screen.getByTestId('item-participant-add')).toBeTruthy();
  });

  it('adds a participant by email and renders the result', async () => {
    items.listParticipants.mockResolvedValue([]);
    items.addParticipant.mockResolvedValue([participant]);
    render(ParticipantsSection, { itemId: 42 });

    await screen.findByTestId('item-participant-add-toggle');
    await fireEvent.click(screen.getByTestId('item-participant-add-toggle'));
    await fireEvent.input(screen.getByTestId('item-participant-email'), {
      target: { value: 'alice@example.test' },
    });
    await fireEvent.click(screen.getByTestId('item-participant-add'));

    await waitFor(() =>
      expect(items.addParticipant).toHaveBeenCalledWith(42, {
        email: 'alice@example.test',
        name: undefined,
      })
    );
    expect(await screen.findByTestId('item-participant-7')).toBeTruthy();
    expect(screen.queryByTestId('item-participant-email')).toBeNull();
  });

  it('removes a participant', async () => {
    items.listParticipants.mockResolvedValue([participant]);
    items.removeParticipant.mockResolvedValue(undefined);
    render(ParticipantsSection, { itemId: 42 });

    await screen.findByTestId('item-participant-remove-7');
    await fireEvent.click(screen.getByTestId('item-participant-remove-7'));

    await waitFor(() => expect(items.removeParticipant).toHaveBeenCalledWith(42, 7));
    await waitFor(() => expect(screen.queryByTestId('item-participant-7')).toBeNull());
  });

  it('hides add and remove controls when read-only', async () => {
    items.listParticipants.mockResolvedValue([participant]);
    render(ParticipantsSection, { itemId: 42, canEdit: false });

    await screen.findByTestId('item-participant-7');
    expect(screen.queryByTestId('item-participant-add-toggle')).toBeNull();
    expect(screen.queryByTestId('item-participant-email')).toBeNull();
    expect(screen.queryByTestId('item-participant-add')).toBeNull();
    expect(screen.queryByTestId('item-participant-remove-7')).toBeNull();
  });
});
