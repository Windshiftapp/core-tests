/** @vitest-environment jsdom */

import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import ZammadSupportWidget from './ZammadSupportWidget.svelte';

const mocks = vi.hoisted(() => ({ overview: vi.fn() }));
vi.mock('../api.js', () => ({ api: { zammadTickets: { workspaceOverview: mocks.overview } } }));
vi.mock('../stores', () => ({ authStore: { currentUser: { timezone: 'UTC' } } }));
vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key, params = {}) => {
    if (key === 'zammad.ticketNumber') return `Zammad #${params.number}`;
    if (key === 'zammad.timeline.change') return `${params.field}: ${params.from} → ${params.to}`;
    if (key === 'zammad.overview.statusWithConnection') return `${params.connection} · ${params.status}`;
    return key;
  },
}));

const deferred = () => {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
};

const overview = (title) => ({
  total: 1,
  active: 1,
  by_status: [{ connection_name: 'Helpdesk', id: 2, name: 'open', count: 1 }],
  tickets: [{ id: title, item_id: 42, item_key: 'OPS-42', ticket_title: title, ticket_number: '123', status: { id: 2, name: 'open' }, group: { id: 7, name: 'Network' }, owner: { id: 9, name: 'Ada' } }],
  recent_changes: [
    { id: `${title}-status`, item_id: 42, item_key: 'OPS-42', ticket_title: title, ticket_number: '123', field: 'status', old_value: { id: 1, name: 'new' }, new_value: { id: 2, name: 'open' }, current_group: { id: 7, name: 'Network' }, current_owner: { id: 9, name: 'Ada' }, observed_at: '2026-09-30T10:00:00Z' },
    { id: `${title}-owner`, item_id: 42, item_key: 'OPS-42', ticket_title: title, ticket_number: '123', field: 'owner', old_value: { id: 1, name: 'Unassigned' }, new_value: { id: 9, name: 'Ada' }, current_group: { id: 7, name: 'Network' }, current_owner: { id: 9, name: 'Ada' }, observed_at: '2026-09-30T10:00:00Z' },
    { id: `${title}-priority`, item_id: 42, field: 'priority' },
  ],
});

beforeEach(() => mocks.overview.mockReset());
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe('Zammad workspace support widget', () => {
  it('renders linked tickets and observed changes with workspace navigation', async () => {
    mocks.overview.mockResolvedValue(overview('VPN access'));
    render(ZammadSupportWidget, { workspaceId: 7 });

    expect(await screen.findByTestId('zammad-support-overview')).toBeInTheDocument();
    expect(mocks.overview).toHaveBeenCalledWith(7, { limit: 5 });
    expect(screen.getByRole('link', { name: 'OPS-42' })).toHaveAttribute('href', '/workspaces/7/items/42');
    expect(screen.getByText('Helpdesk · open: 1')).toBeInTheDocument();
    expect(screen.getAllByText('Network').length).toBeGreaterThan(0);
    expect(screen.getAllByText('Ada').length).toBeGreaterThan(0);
    expect(screen.getByText('zammad.timeline.field.status: new → open')).toBeInTheDocument();
    expect(screen.getByText('zammad.timeline.field.owner: zammad.unassignedOwner → Ada')).toBeInTheDocument();
    expect(screen.queryByText('priority')).not.toBeInTheDocument();
  });

  it('shows an API permission error and recovers on retry', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
    mocks.overview.mockRejectedValueOnce(new Error('403')).mockResolvedValueOnce(overview('Recovered'));
    render(ZammadSupportWidget, { workspaceId: 7 });

    expect(await screen.findByText('zammad.overview.loadFailed')).toBeInTheDocument();
    expect(screen.queryByTestId('zammad-support-overview')).not.toBeInTheDocument();
    await fireEvent.click(screen.getByRole('button', { name: 'common.retry' }));
    expect((await screen.findAllByText('Recovered')).length).toBeGreaterThan(0);
    expect(mocks.overview).toHaveBeenCalledTimes(2);
    expect(consoleError).toHaveBeenCalledOnce();
  });

  it('ignores a stale error from a previous workspace', async () => {
    const old = deferred();
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
    mocks.overview.mockReturnValueOnce(old.promise).mockResolvedValueOnce(overview('Current workspace'));
    const view = render(ZammadSupportWidget, { workspaceId: 7 });
    await waitFor(() => expect(mocks.overview).toHaveBeenCalledWith(7, { limit: 5 }));

    await view.rerender({ workspaceId: 8 });
    expect((await screen.findAllByText('Current workspace')).length).toBeGreaterThan(0);
    old.reject(new Error('403'));
    await waitFor(() => expect(mocks.overview).toHaveBeenCalledTimes(2));
    expect(screen.getAllByText('Current workspace').length).toBeGreaterThan(0);
    expect(screen.queryByText('zammad.overview.loadFailed')).not.toBeInTheDocument();
    expect(consoleError).not.toHaveBeenCalled();
  });
});
