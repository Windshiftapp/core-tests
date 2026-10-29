import { render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api/sla.js', () => ({
  sla: {
        getItemSLA: vi.fn(),
        getWarningThresholds: vi.fn(),
      },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key, params) => (params ? `${key} ${JSON.stringify(params)}` : key),
}));

vi.mock('../../utils/dateFormatter.js', () => ({
  formatInstant: (_value, timezone, options) => `${timezone} ${options.hour}:${options.minute}`,
}));

import { sla } from '../../api/sla.js';
import { invalidateSLAState, invalidateSLAThresholds, resetSLAState } from './slaState.js';
import SLABadge from './SLABadge.svelte';

function cycle(overrides = {}) {
  return {
    metric_id: 1,
    metric_name: 'First response',
    display_format: 'time',
    ongoing: {
      cycle_no: 1,
      breached: false,
      paused: false,
      within_calendar_hours: true,
      elapsed_ms: 1000,
      goal_duration_ms: 3600000,
      remaining_ms: 3599000,
      ...overrides,
    },
  };
}

describe('SLABadge', () => {
  let nextItemId = 1000;
  const itemId = () => nextItemId++;

  beforeEach(() => {
    vi.clearAllMocks();
    resetSLAState();
    sla.getWarningThresholds.mockResolvedValue([]);
  });

  it('shows a breached badge for a breached cycle', async () => {
    sla.getItemSLA.mockResolvedValue([cycle({ breached: true, remaining_ms: -1000 })]);
    render(SLABadge, { itemId: itemId(), workspaceId: 5 });
    expect(await screen.findByText('items.sla.breached')).toBeTruthy();
  });

  it('shows a paused badge', async () => {
    sla.getItemSLA.mockResolvedValue([cycle({ paused: true })]);
    render(SLABadge, { itemId: itemId(), workspaceId: 5 });
    expect(await screen.findByText('items.sla.paused')).toBeTruthy();
  });

  it('shows a paused badge for an active cycle outside service hours', async () => {
    sla.getItemSLA.mockResolvedValue([cycle({ within_calendar_hours: false })]);
    render(SLABadge, { itemId: itemId(), workspaceId: 5 });
    const badge = await screen.findByTestId('sla-badge-paused-outside-hours');
    expect(badge.textContent.trim()).toBe('items.sla.paused');
  });

  it('keeps paused state distinct from outside-calendar time', async () => {
    sla.getItemSLA.mockResolvedValue([cycle({ paused: true, within_calendar_hours: false })]);
    render(SLABadge, { itemId: itemId(), workspaceId: 5 });
    expect(await screen.findByText('items.sla.paused')).toBeTruthy();
    expect(screen.queryByTestId('sla-badge-paused-outside-hours')).toBeNull();
  });

  it('renders no badge for a cycle with no matched goal', async () => {
    sla.getItemSLA.mockResolvedValue([
      cycle({ goal_duration_ms: 0, remaining_ms: null, within_calendar_hours: false }),
    ]);
    render(SLABadge, { itemId: itemId(), workspaceId: 5 });
    await waitFor(() => expect(sla.getItemSLA).toHaveBeenCalled());
    expect(screen.queryByTestId('sla-badge-paused-outside-hours')).toBeNull();
    expect(screen.queryByText('items.sla.paused')).toBeNull();
  });

  it('shows a warning badge once an active threshold is reached', async () => {
    sla.getWarningThresholds.mockResolvedValue([
      { id: 1, percent: 50, metric_id: null, is_active: true },
    ]);
    sla.getItemSLA.mockResolvedValue([cycle({ elapsed_ms: 2000000, remaining_ms: 1600000 })]);
    render(SLABadge, { itemId: itemId(), workspaceId: 5 });
    expect(await screen.findByText('items.sla.warning')).toBeTruthy();
  });

  it('shows a warning when any configured threshold has been reached', async () => {
    sla.getWarningThresholds.mockResolvedValue([
      { id: 1, percent: 50, metric_id: null, is_active: true },
      { id: 2, percent: 75, metric_id: null, is_active: true },
    ]);
    sla.getItemSLA.mockResolvedValue([cycle({ elapsed_ms: 2160000, remaining_ms: 1440000 })]);
    render(SLABadge, { itemId: itemId(), workspaceId: 5 });
    expect(await screen.findByText('items.sla.warning')).toBeTruthy();
  });

  it('renders due-date deadlines with the calendar timezone and time', async () => {
    sla.getItemSLA.mockResolvedValue([
      {
        ...cycle(),
        display_format: 'due_date',
        ongoing: {
          ...cycle().ongoing,
          next_deadline_at: '2026-09-28T09:00:00Z',
          calendar_timezone: 'Asia/Tokyo',
        },
      },
    ]);
    render(SLABadge, { itemId: itemId(), workspaceId: 5 });
    expect(await screen.findByText(/Asia\/Tokyo numeric:2-digit/)).toBeTruthy();
  });

  it('reloads mounted badges after item SLA state is invalidated', async () => {
    sla.getItemSLA
      .mockResolvedValueOnce([cycle()])
      .mockResolvedValueOnce([cycle({ breached: true })]);
    render(SLABadge, { itemId: itemId(), workspaceId: 5 });
    await screen.findByText('60m');

    window.dispatchEvent(new CustomEvent('refresh-work-items'));

    expect(await screen.findByText('items.sla.breached')).toBeTruthy();
    expect(sla.getItemSLA).toHaveBeenCalledTimes(2);
  });

  it('reloads when the board invalidates and bumps the refresh token', async () => {
    sla.getItemSLA
      .mockResolvedValueOnce([cycle()])
      .mockResolvedValueOnce([cycle({ breached: true })]);
    const id = itemId();
    const { rerender } = render(SLABadge, { itemId: id, workspaceId: 5, refreshToken: 0 });
    await screen.findByText('60m');

    invalidateSLAState();
    await rerender({ itemId: id, workspaceId: 5, refreshToken: 1 });

    expect(await screen.findByText('items.sla.breached')).toBeTruthy();
    expect(sla.getItemSLA).toHaveBeenCalledTimes(2);
  });

  it('does not let a pre-invalidation response overwrite refreshed cache state', async () => {
    let resolveOldRequest;
    sla.getItemSLA
      .mockImplementationOnce(() => new Promise((resolve) => (resolveOldRequest = resolve)))
      .mockResolvedValueOnce([cycle({ breached: true })]);
    const id = itemId();
    render(SLABadge, { itemId: id, workspaceId: 5 });
    window.dispatchEvent(new CustomEvent('refresh-work-items'));
    expect(await screen.findByText('items.sla.breached')).toBeTruthy();

    resolveOldRequest([cycle()]);
    render(SLABadge, { itemId: id, workspaceId: 5 });

    expect(screen.getByText('items.sla.breached')).toBeTruthy();
    expect(sla.getItemSLA).toHaveBeenCalledTimes(2);
  });

  it('reloads thresholds after an in-tab threshold mutation', async () => {
    sla.getItemSLA.mockResolvedValue([cycle({ elapsed_ms: 3_000_000 })]);
    sla.getWarningThresholds
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce([{ id: 1, percent: 75, is_active: true }]);
    render(SLABadge, { itemId: itemId(), workspaceId: 5 });
    await waitFor(() => expect(sla.getWarningThresholds).toHaveBeenCalledTimes(1));

    invalidateSLAThresholds(5);

    expect(await screen.findByText('items.sla.warning')).toBeTruthy();
    expect(sla.getWarningThresholds).toHaveBeenCalledTimes(2);
  });

  it('shows the remaining time when no other signal applies', async () => {
    sla.getItemSLA.mockResolvedValue([cycle({ remaining_ms: 3540000 })]);
    render(SLABadge, { itemId: itemId(), workspaceId: 5 });
    expect(await screen.findByText('59m')).toBeTruthy();
  });

  it('renders nothing when the item has no cycles', async () => {
    sla.getItemSLA.mockResolvedValue([]);
    render(SLABadge, { itemId: itemId(), workspaceId: 5 });
    // No badge text should appear for any SLA state.
    await Promise.resolve();
    expect(screen.queryByText('items.sla.breached')).toBeNull();
    expect(screen.queryByText('items.sla.paused')).toBeNull();
    expect(screen.queryByText('items.sla.warning')).toBeNull();
  });
});
