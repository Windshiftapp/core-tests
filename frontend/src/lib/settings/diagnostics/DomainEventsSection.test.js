import { cleanup, fireEvent, render, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('../../api/diagnostics.js', () => ({
  getDomainEventDiagnostics: vi.fn(),
  replayDomainEvent: vi.fn(),
  skipDomainEvent: vi.fn(),
}));

vi.mock('../../stores/toasts.svelte.js', () => ({
  successToast: vi.fn(),
  errorToast: vi.fn(),
}));

import { getDomainEventDiagnostics, replayDomainEvent } from '../../api/diagnostics.js';
import { successToast } from '../../stores/toasts.svelte.js';
import DomainEventsSection from './DomainEventsSection.svelte';

describe('DomainEventsSection', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    getDomainEventDiagnostics.mockResolvedValue({
      generated_at: '2026-08-27T19:00:00Z',
      filter: {},
      consumers: [
        {
          consumer_key: 'actions.items.v1',
          pending: 3,
          retrying: 1,
          active_leases: 2,
          expired_leases: 0,
          terminal_failures: 1,
          blocked_aggregates: 1,
          oldest_pending_age_seconds: 120,
        },
      ],
      failures: [
        {
          event_id: 42,
          event_key: 'event-42',
          workspace_id: 7,
          consumer_key: 'actions.items.v1',
          aggregate_type: 'item',
          aggregate_id: '99',
          aggregate_sequence: 4,
          event_type: 'item.updated',
          last_error: 'permanent target failure',
          failed_at: '2026-08-27T18:59:00Z',
        },
      ],
    });
    replayDomainEvent.mockResolvedValue({
      ordering_impact: 'Delivery is eligible for retry.',
    });
  });

  afterEach(() => cleanup());

  test('filters diagnostics and replays a failed delivery with a reason', async () => {
    const view = render(DomainEventsSection);
    await waitFor(() => expect(getDomainEventDiagnostics).toHaveBeenCalledTimes(1));
    expect(view.getAllByText('actions.items.v1')).toHaveLength(2);
    expect(view.getByText('permanent target failure')).toBeInTheDocument();

    await fireEvent.input(view.getByTestId('domain-events-consumer-filter'), {
      target: { value: 'actions.items.v1' },
    });
    await fireEvent.input(view.getByTestId('domain-events-workspace-filter'), {
      target: { value: '7' },
    });
    await fireEvent.click(view.getByTestId('domain-events-apply-filter'));
    await waitFor(() => {
      expect(getDomainEventDiagnostics).toHaveBeenLastCalledWith({
        consumerKey: 'actions.items.v1',
        workspaceId: '7',
        limit: 100,
      });
    });

    await fireEvent.input(view.getByTestId('domain-event-reason-42'), {
      target: { value: 'configuration repaired' },
    });
    await fireEvent.click(view.getByTestId('domain-event-replay-42'));
    await waitFor(() => {
      expect(replayDomainEvent).toHaveBeenCalledWith(
        42,
        'actions.items.v1',
        'configuration repaired'
      );
    });
    expect(successToast).toHaveBeenCalledWith('Delivery is eligible for retry.');
  });
});
