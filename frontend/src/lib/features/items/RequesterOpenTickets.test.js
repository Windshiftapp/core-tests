import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { requesterOpenTickets, findSimilar } = vi.hoisted(() => ({
  requesterOpenTickets: vi.fn(),
  findSimilar: vi.fn(),
}));

vi.mock('../../api/items.js', () => ({
  items: {
        requesterOpenTickets: (id, options) => requesterOpenTickets(id, options),
      },
}))

vi.mock('../../api/ai.js', () => ({
  ai: {
        status: vi.fn().mockResolvedValue({ available: false }),
        findSimilar: (id) => findSimilar(id),
      },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

import RequesterOpenTickets from './RequesterOpenTickets.svelte';
import { aiStore } from '../../stores/aiStore.svelte.js';
import { items } from '../../api/items.js';
import { ai } from '../../api/ai.js';


const candidate = {
  id: 42,
  workspace_id: 7,
  workspace_key: 'SUP',
  workspace_item_number: 12,
  title: 'Same printer issue',
  status_name: 'In Progress',
};

beforeEach(() => {
  requesterOpenTickets.mockReset();
  findSimilar.mockReset();
  // Reset the shared AI store so a previous test's availability cannot leak.
  aiStore.hydrate({ available: false, features: {} });
});

describe('RequesterOpenTickets panel (WI-1548)', () => {
  it('renders candidate tickets as links when the requester has open tickets', async () => {
    requesterOpenTickets.mockResolvedValue([candidate]);

    render(RequesterOpenTickets, { itemId: 9 });

    expect(await screen.findByTestId('requester-open-tickets')).toBeInTheDocument();
    // The section header uses the shared uppercase subtle style, not a bold
    // body-text heading.
    const heading = screen.getByText('items.requesterOpenTickets');
    expect(heading).toHaveClass('uppercase');
    expect(heading).toHaveClass('tracking-wider');
    expect(heading).toHaveClass('text-xs');
    const link = screen.getByTestId('requester-open-ticket-42');
    expect(link).toHaveAttribute('href', '/workspaces/7/items/42');
    expect(link).toHaveTextContent('SUP-12');
    expect(link).toHaveTextContent('Same printer issue');
    expect(screen.getByText('In Progress')).toBeInTheDocument();
    expect(requesterOpenTickets).toHaveBeenCalledWith(9, expect.objectContaining({ signal: expect.anything() }));
  });

  it('shows an empty state when the requester has no other open tickets', async () => {
    requesterOpenTickets.mockResolvedValue([]);

    render(RequesterOpenTickets, { itemId: 9 });

    expect(await screen.findByText('items.requesterOpenTicketsEmpty')).toBeInTheDocument();
    expect(screen.queryByTestId('requester-open-ticket-42')).not.toBeInTheDocument();
  });

  it('hands a candidate to the merge action through the overflow menu when the user can edit', async () => {
    requesterOpenTickets.mockResolvedValue([candidate]);
    const onrequestmerge = vi.fn();

    render(RequesterOpenTickets, { itemId: 9, canEdit: true, onrequestmerge });

    const menuTrigger = await screen.findByTestId('requester-open-ticket-menu-42');
    // The merge action is tucked away until the overflow menu opens.
    expect(screen.queryByTestId('requester-open-ticket-merge-42')).not.toBeInTheDocument();

    await fireEvent.click(menuTrigger);

    const mergeItem = await screen.findByTestId('requester-open-ticket-merge-42');
    expect(mergeItem).toHaveTextContent('items.mergeIntoThisTicket');
    await fireEvent.click(mergeItem);

    expect(onrequestmerge).toHaveBeenCalledTimes(1);
    expect(onrequestmerge).toHaveBeenCalledWith(expect.objectContaining({ id: 42 }));
  });

  it('hides the merge action without edit permission', async () => {
    requesterOpenTickets.mockResolvedValue([candidate]);

    render(RequesterOpenTickets, { itemId: 9, canEdit: false, onrequestmerge: vi.fn() });

    await screen.findByTestId('requester-open-tickets');
    expect(screen.queryByTestId('requester-open-ticket-menu-42')).not.toBeInTheDocument();
    expect(screen.queryByTestId('requester-open-ticket-merge-42')).not.toBeInTheDocument();
  });

  it('hides the find-duplicates action when AI is unavailable', async () => {
    requesterOpenTickets.mockResolvedValue([candidate]);

    render(RequesterOpenTickets, { itemId: 9, canEdit: true, onrequestmerge: vi.fn() });

    await screen.findByTestId('requester-open-tickets');
    expect(screen.queryByTestId('requester-find-duplicates')).not.toBeInTheDocument();
  });

  it('adds AI-found duplicates temporarily and dedupes the requester list', async () => {
    requesterOpenTickets.mockResolvedValue([candidate]);
    findSimilar.mockResolvedValue({
      similar_items: [
        {
          item_id: 42,
          item_key: 'SUP-12',
          title: 'Same printer issue',
          status_name: 'In Progress',
          similarity: 'closely_related',
          reason: 'already listed',
          workspace_id: 7,
        },
        {
          item_id: 77,
          item_key: 'SUP-77',
          title: 'Printer jams on A4',
          status_name: 'Open',
          similarity: 'duplicate',
          reason: 'Same printer model and fault',
          workspace_id: 7,
          creator_portal_customer_id: 5,
        },
      ],
    });
    aiStore.hydrate({ available: true, features: { find_similar: { enabled: true } } });
    const onrequestmerge = vi.fn();

    render(RequesterOpenTickets, { itemId: 9, canEdit: true, onrequestmerge });

    await screen.findByTestId('requester-open-tickets');
    await fireEvent.click(screen.getByTestId('requester-find-duplicates'));

    await waitFor(() => expect(findSimilar).toHaveBeenCalledWith(9));
    const aiRow = await screen.findByTestId('requester-ai-duplicate-77');
    expect(aiRow).toHaveTextContent('Printer jams on A4');
    expect(aiRow).toHaveTextContent('Same printer model and fault');
    // The requester candidate is not duplicated as an AI row.
    expect(screen.queryByTestId('requester-ai-duplicate-42')).not.toBeInTheDocument();
    expect(screen.getByText('items.aiSuggestionsTemporary')).toBeInTheDocument();

    await fireEvent.click(screen.getByTestId('requester-ai-duplicate-menu-77'));
    await fireEvent.click(await screen.findByTestId('requester-ai-duplicate-merge-77'));
    expect(onrequestmerge).toHaveBeenCalledWith(
      expect.objectContaining({ id: 77, creator_portal_customer_id: 5 })
    );
  });

  it('surfaces a failed AI duplicate search', async () => {
    requesterOpenTickets.mockResolvedValue([candidate]);
    findSimilar.mockRejectedValue(new Error('ai offline'));
    aiStore.hydrate({ available: true, features: { find_similar: { enabled: true } } });

    render(RequesterOpenTickets, { itemId: 9, canEdit: true, onrequestmerge: vi.fn() });

    await screen.findByTestId('requester-open-tickets');
    await fireEvent.click(screen.getByTestId('requester-find-duplicates'));

    const error = await screen.findByTestId('requester-find-duplicates-error');
    expect(error).toHaveTextContent('ai offline');
  });
});
