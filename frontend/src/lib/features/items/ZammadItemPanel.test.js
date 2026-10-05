/** @vitest-environment jsdom */

import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { tick } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import ZammadItemPanel from './ZammadItemPanel.svelte';

const mocks = vi.hoisted(() => ({
  connections: vi.fn(),
  links: vi.fn(),
  history: vi.fn(),
  refresh: vi.fn(),
  successToast: vi.fn(),
  errorToast: vi.fn(),
}));
vi.mock('../../api.js', () => ({
  api: {
    zammadConnections: { forWorkspace: mocks.connections },
    zammadTickets: { forItem: mocks.links, history: mocks.history, refresh: mocks.refresh },
  },
}));
vi.mock('../../stores', () => ({ authStore: { currentUser: { timezone: 'UTC' } } }));
vi.mock('../../stores/toasts.svelte.js', () => ({
  successToast: mocks.successToast,
  errorToast: mocks.errorToast,
}));
vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key, params = {}) => {
    if (key === 'zammad.ticketNumber') return `Zammad #${params.number}`;
    if (key === 'zammad.overview.statusIdFallback') return `Status ID ${params.id}`;
    if (key === 'zammad.timeline.change') return `${params.field}: ${params.from} → ${params.to}`;
    return key;
  },
}));

const ticket = (overrides = {}) => ({
  id: 'link-1',
  connection_id: 'dev',
  connection_name: 'Helpdesk',
  ticket_id: 42,
  ticket_number: '123',
  ticket_title: 'VPN access',
  ticket_url: 'https://zammad.example.test/#ticket/zoom/42',
  group_name: 'Network',
  owner_name: 'Ada',
  sync_state: 'linked',
  last_status_id: 4,
  last_status_name: 'closed',
  closed: true,
  ...overrides,
});

beforeEach(() => {
  vi.clearAllMocks();
  mocks.connections.mockResolvedValue([
    { id: 'dev', name: 'Helpdesk', auth_method: 'api_token', ready: true },
  ]);
  mocks.links.mockResolvedValue([ticket()]);
  mocks.history.mockResolvedValue({ events: [] });
  mocks.refresh.mockResolvedValue(ticket());
});
afterEach(cleanup);

describe('Zammad item panel observations', () => {
  it('keeps the current sidebar actions while showing ticket title, status and observed changes', async () => {
    mocks.history.mockResolvedValue({
      events: [
        {
          id: 'change-1',
          field: 'owner',
          ticket_number: '123',
          old_value: { id: 1, name: 'Unassigned' },
          new_value: { id: 9, name: 'Ada' },
          observed_at: '2026-09-30T10:00:00Z',
        },
        { id: 'ignored', field: 'priority' },
      ],
    });
    render(ZammadItemPanel, { itemId: 5, workspaceId: 7, canEdit: true });

    const card = await screen.findByTestId('zammad-ticket-card-link-1');
    expect(within(card).getByRole('link', { name: 'VPN access' })).toHaveAttribute(
      'href',
      'https://zammad.example.test/#ticket/zoom/42'
    );
    expect(within(card).getByText('closed')).toBeInTheDocument();
    expect(within(card).getByRole('button', { name: 'zammad.refreshTicket' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'zammad.createTicket' })).toBeInTheDocument();
    await fireEvent.click(screen.getByRole('button', { name: 'zammad.timeline.toggle' }));
    expect(
      await screen.findByText('zammad.timeline.field.owner: zammad.unassignedOwner → Ada')
    ).toBeInTheDocument();
    expect(mocks.history).toHaveBeenCalledWith(5, { limit: 6 });
    expect(screen.queryByText('priority')).not.toBeInTheDocument();
  });

  it('reloads hidden history after a link change invalidates an in-flight response', async () => {
    let resolveOldHistory;
    const oldHistory = new Promise((resolve) => {
      resolveOldHistory = resolve;
    });
    mocks.history.mockReturnValueOnce(oldHistory).mockResolvedValueOnce({
      events: [
        {
          id: 'fresh-change',
          field: 'status',
          ticket_number: '123',
          old_value: { id: 2, name: 'open' },
          new_value: { id: 4, name: 'freshly closed' },
          observed_at: '2026-10-04T10:00:00Z',
        },
      ],
    });
    render(ZammadItemPanel, { itemId: 5, workspaceId: 7 });
    await screen.findByTestId('zammad-ticket-card-link-1');
    await fireEvent.click(screen.getByRole('button', { name: 'zammad.timeline.toggle' }));
    expect(mocks.history).toHaveBeenCalledTimes(1);
    await fireEvent.click(screen.getByRole('button', { name: 'zammad.timeline.toggle' }));
    window.dispatchEvent(new CustomEvent('item-zammad-links-changed', { detail: { itemId: 5 } }));
    await waitFor(() => expect(mocks.links).toHaveBeenCalledTimes(2));
    await screen.findByTestId('zammad-ticket-card-link-1');
    resolveOldHistory({ events: [] });
    await oldHistory;
    await tick();
    await fireEvent.click(screen.getByRole('button', { name: 'zammad.timeline.toggle' }));
    await waitFor(() => expect(mocks.history).toHaveBeenCalledTimes(2));
    expect(
      await screen.findByText('zammad.timeline.field.status: open → freshly closed')
    ).toBeInTheDocument();
  });

  it('shows a nameless observed status by ID', async () => {
    mocks.links.mockResolvedValue([
      ticket({ last_status_id: 9, last_status_name: '', closed: false }),
    ]);
    render(ZammadItemPanel, { itemId: 5, workspaceId: 7 });
    const card = await screen.findByTestId('zammad-ticket-card-link-1');
    expect(within(card).getByText('Status ID 9')).toBeInTheDocument();
  });

  it('retains the sidebar card if reloading after a successful refresh fails', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
    mocks.links.mockResolvedValueOnce([ticket()]).mockRejectedValueOnce(new Error('reload failed'));
    render(ZammadItemPanel, { itemId: 5, workspaceId: 7, canEdit: true });
    const card = await screen.findByTestId('zammad-ticket-card-link-1');
    await fireEvent.click(within(card).getByRole('button', { name: 'zammad.refreshTicket' }));
    await waitFor(() =>
      expect(mocks.errorToast).toHaveBeenCalledWith('zammad.ticketReloadAfterChangeFailed')
    );
    expect(screen.getByTestId('zammad-ticket-card-link-1')).toBeInTheDocument();
    expect(screen.queryByText('zammad.loadLinksFailed')).not.toBeInTheDocument();
    expect(mocks.successToast).not.toHaveBeenCalled();
    consoleError.mockRestore();
  });
});
