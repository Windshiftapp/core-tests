import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { tick } from 'svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api/sla.js', () => ({
  sla: { getItemSLA: vi.fn() },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key, params) => (params ? `${key} ${JSON.stringify(params)}` : key),
}));

vi.mock('../../utils/dateFormatter.js', () => ({
  formatInstant: (_value, timezone, options) => `${timezone} ${options.hour}:${options.minute}`,
}));

import { sla } from '../../api/sla.js';
import ItemSLAPanel from './ItemSLAPanel.svelte';

const stateWithCycle = [
  {
    metric_id: 1,
    metric_name: 'First response',
    display_format: 'time',
    recalculating: false,
    ongoing: {
      cycle_no: 1,
      breached: true,
      paused: false,
      within_calendar_hours: true,
      elapsed_ms: 7200000,
      goal_duration_ms: 3600000,
      remaining_ms: -3600000,
      coverage: {
        reference: 'team_service_hours',
        discrepancy: 'sla_wider',
        note: 'sla_outside_team_service_hours',
      },
    },
    completed: [{ cycle_no: 1, elapsed_ms: 100, breached: true }],
  },
];

function deferred() {
  let resolve;
  const promise = new Promise((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
}

describe('ItemSLAPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('hides itself when the item has no SLA cycles', async () => {
    sla.getItemSLA.mockResolvedValue([]);
    render(ItemSLAPanel, { itemId: 42 });

    await waitFor(() => expect(sla.getItemSLA).toHaveBeenCalledWith(42));
    expect(screen.queryByTestId('item-sla-panel')).toBeNull();
  });

  it('renders the ongoing cycle with breach state and completed history', async () => {
    sla.getItemSLA.mockResolvedValue(stateWithCycle);
    render(ItemSLAPanel, { itemId: 42 });

    expect(await screen.findByTestId('item-sla-panel')).toBeTruthy();
    expect(screen.getByTestId('item-sla-metric-1')).toBeTruthy();
    expect(screen.getByText('First response')).toBeTruthy();
    // Breached badge appears on both the ongoing and completed cycle.
    expect(screen.getAllByText('items.sla.breached').length).toBeGreaterThanOrEqual(2);
    expect(screen.getByTestId('item-sla-coverage-1')).toBeTruthy();
    // Being inside service hours is the default; only the exception is shown.
    expect(screen.queryByText('items.sla.withinHours')).toBeNull();
  });

  it('shows a paused badge for time outside the SLA service hours', async () => {
    sla.getItemSLA.mockResolvedValue([
      {
        ...stateWithCycle[0],
        ongoing: { ...stateWithCycle[0].ongoing, breached: false, within_calendar_hours: false },
      },
    ]);
    render(ItemSLAPanel, { itemId: 42 });

    const badge = await screen.findByTestId('item-sla-paused-outside-hours-1');
    expect(badge.textContent.trim()).toBe('items.sla.paused');
    expect(screen.queryByText('items.sla.withinHours')).toBeNull();
  });

  it('explains the outside-hours pause with a service-hours tooltip', async () => {
    sla.getItemSLA.mockResolvedValue([
      {
        ...stateWithCycle[0],
        ongoing: { ...stateWithCycle[0].ongoing, breached: false, within_calendar_hours: false },
      },
    ]);
    render(ItemSLAPanel, { itemId: 42 });

    const badge = await screen.findByTestId('item-sla-paused-outside-hours-1');
    fireEvent.pointerEnter(badge.parentElement);

    await waitFor(() => expect(screen.getByTestId('tooltip').textContent).toContain('items.sla.outsideHours'));
  });

  it('reports a metric whose goal did not match instead of a misleading timer', async () => {
    sla.getItemSLA.mockResolvedValue([
      {
        ...stateWithCycle[0],
        ongoing: {
          ...stateWithCycle[0].ongoing,
          goal_duration_ms: 0,
          remaining_ms: null,
          within_calendar_hours: false,
        },
      },
    ]);
    render(ItemSLAPanel, { itemId: 42 });

    expect(await screen.findByTestId('item-sla-no-goal-1')).toBeTruthy();
    expect(screen.queryByTestId('item-sla-remaining-1')).toBeNull();
    expect(screen.queryByTestId('item-sla-paused-outside-hours-1')).toBeNull();
  });

  it('renders due-date deadlines with the calendar timezone and time', async () => {
    sla.getItemSLA.mockResolvedValue([
      {
        ...stateWithCycle[0],
        display_format: 'due_date',
        ongoing: {
          ...stateWithCycle[0].ongoing,
          breached: false,
          next_deadline_at: '2026-09-28T09:00:00Z',
          calendar_timezone: 'Asia/Tokyo',
        },
      },
    ]);
    render(ItemSLAPanel, { itemId: 42 });

    expect(await screen.findByText(/Asia\/Tokyo numeric:2-digit/)).toBeTruthy();
  });

  it('reloads when item mutations fire refresh-work-items', async () => {
    sla.getItemSLA.mockResolvedValue(stateWithCycle);
    render(ItemSLAPanel, { itemId: 42 });
    await screen.findByTestId('item-sla-panel');

    sla.getItemSLA.mockClear();
    window.dispatchEvent(new CustomEvent('refresh-work-items'));

    await waitFor(() => expect(sla.getItemSLA).toHaveBeenCalledWith(42));
  });

  it('refreshes an ongoing timer on an interval without item mutations', async () => {
    vi.useFakeTimers();
    try {
      sla.getItemSLA.mockResolvedValue(stateWithCycle);
      render(ItemSLAPanel, { itemId: 42 });

      await vi.advanceTimersByTimeAsync(0);
      expect(sla.getItemSLA).toHaveBeenCalledTimes(1);

      await vi.advanceTimersByTimeAsync(30000);
      expect(sla.getItemSLA).toHaveBeenCalledTimes(2);

      await vi.advanceTimersByTimeAsync(30000);
      expect(sla.getItemSLA).toHaveBeenCalledTimes(3);
    } finally {
      vi.useRealTimers();
    }
  });

  it('does not poll when no timer is running', async () => {
    vi.useFakeTimers();
    try {
      sla.getItemSLA.mockResolvedValue([
        { ...stateWithCycle[0], ongoing: null },
      ]);
      render(ItemSLAPanel, { itemId: 42 });

      await vi.advanceTimersByTimeAsync(0);
      expect(sla.getItemSLA).toHaveBeenCalledTimes(1);

      await vi.advanceTimersByTimeAsync(120000);
      expect(sla.getItemSLA).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it('ignores a stale response that resolves after the item changed', async () => {
    const first = deferred();
    const second = deferred();
    sla.getItemSLA.mockImplementation((id) => (id === 1 ? first.promise : second.promise));

    const { rerender } = render(ItemSLAPanel, { props: { itemId: 1 } });
    await waitFor(() => expect(sla.getItemSLA).toHaveBeenCalledWith(1));
    await rerender({ itemId: 2 });
    await waitFor(() => expect(sla.getItemSLA).toHaveBeenCalledWith(2));

    second.resolve([{ ...stateWithCycle[0], metric_name: 'Newer metric' }]);
    expect(await screen.findByText('Newer metric')).toBeTruthy();

    first.resolve([{ ...stateWithCycle[0], metric_name: 'Older metric' }]);
    await tick();
    await new Promise((resolve) => queueMicrotask(resolve));
    expect(screen.queryByText('Older metric')).toBeNull();
    expect(screen.getByText('Newer metric')).toBeTruthy();
  });
});
