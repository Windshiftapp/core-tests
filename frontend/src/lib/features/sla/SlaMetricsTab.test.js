import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api/sla.js', () => ({
  sla: {
        getMetrics: vi.fn(),
        getAvailableCalendars: vi.fn(),
        createMetric: vi.fn(),
        updateMetric: vi.fn(),
        deleteMetric: vi.fn(),
      },
}))

vi.mock('../../api/workflows.js', () => ({
  statuses: { getAll: vi.fn() },
  statusCategories: { getAll: vi.fn() },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('../../stores/toasts.svelte.js', () => ({
  successToast: vi.fn(),
  errorToast: vi.fn(),
}));

vi.mock('../../composables/useConfirm.js', () => ({
  confirm: vi.fn().mockResolvedValue(false),
}));

import { sla } from '../../api/sla.js';
import { statusCategories, statuses } from '../../api/workflows.js';
import SlaMetricsTab from './SlaMetricsTab.svelte';

const metrics = [
  {
    id: 1,
    name: 'First response',
    display_format: 'time',
    position: 0,
    is_active: true,
    conditions: [{ phase: 'start', position: 0, condition_type: 'created', config: {} }],
    goals: [],
  },
];

describe('SlaMetricsTab', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sla.getMetrics.mockResolvedValue(metrics);
    sla.getAvailableCalendars.mockResolvedValue([]);
    sla.updateMetric.mockResolvedValue(metrics[0]);
    statuses.getAll.mockResolvedValue([]);
    statusCategories.getAll.mockResolvedValue([]);
  });

  it('lists metrics for the workspace', async () => {
    render(SlaMetricsTab, { workspaceId: 5 });

    await waitFor(() => expect(sla.getMetrics).toHaveBeenCalledWith(5));
    expect(await screen.findByTestId('sla-metric-row-1')).toBeTruthy();
    expect(screen.getByText('First response')).toBeTruthy();
  });

  it('ignores metrics from a previous workspace after switching workspaces', async () => {
    let resolveFirst;
    sla.getMetrics
      .mockImplementationOnce(() => new Promise((resolve) => (resolveFirst = resolve)))
      .mockResolvedValueOnce([{ ...metrics[0], id: 2, name: 'Second workspace metric' }]);
    const view = render(SlaMetricsTab, { workspaceId: 5 });
    await waitFor(() => expect(sla.getMetrics).toHaveBeenCalledWith(5));

    await view.rerender({ workspaceId: 6 });
    expect(await screen.findByText('Second workspace metric')).toBeTruthy();
    resolveFirst([{ ...metrics[0], name: 'Stale workspace metric' }]);

    await Promise.resolve();
    expect(screen.queryByText('Stale workspace metric')).toBeNull();
    expect(screen.getByText('Second workspace metric')).toBeTruthy();
  });

  it('refreshes the metric list after a mutation without a page reload', async () => {
    let current = metrics;
    sla.getMetrics.mockImplementation(() => Promise.resolve(current));
    sla.updateMetric.mockImplementation(() => {
      current = [{ ...metrics[0], name: 'Renamed metric' }];
      return Promise.resolve(metrics[0]);
    });
    render(SlaMetricsTab, { workspaceId: 5 });
    await screen.findByTestId('sla-metric-row-1');

    await fireEvent.click(screen.getByTestId('sla-metric-active-toggle-1'));

    expect(await screen.findByText('Renamed metric')).toBeTruthy();
    expect(screen.queryByText('First response')).toBeNull();
    expect(sla.getMetrics).toHaveBeenLastCalledWith(5);
  });

  it('persists an active-toggle change through the metric payload', async () => {
    render(SlaMetricsTab, { workspaceId: 5 });
    await screen.findByTestId('sla-metric-row-1');

    await fireEvent.click(screen.getByTestId('sla-metric-active-toggle-1'));

    await waitFor(() => expect(sla.updateMetric).toHaveBeenCalled());
    const [workspaceArg, metricID, payload] = sla.updateMetric.mock.calls[0];
    expect(workspaceArg).toBe(5);
    expect(metricID).toBe(1);
    expect(payload.is_active).toBe(false);
    expect(payload.name).toBe('First response');
    expect(payload.conditions).toHaveLength(1);
  });

  it('sends one target with only supported fields when toggling a metric with goals', async () => {
    const metricWithGoal = {
      ...metrics[0],
      goals: [
        {
          position: 0,
          ql_query: 'priority = High',
          import_status: 'native',
          targets: [
            { position: 0, target_ms: 3600000, calendar_id: 1 },
            { position: 1, target_ms: 7200000, calendar_id: 1 },
          ],
        },
      ],
    };
    sla.getMetrics.mockResolvedValue([metricWithGoal]);

    render(SlaMetricsTab, { workspaceId: 5 });
    await screen.findByTestId('sla-metric-row-1');

    await fireEvent.click(screen.getByTestId('sla-metric-active-toggle-1'));

    await waitFor(() => expect(sla.updateMetric).toHaveBeenCalled());
    const payload = sla.updateMetric.mock.calls[0][2];
    expect(payload.goals).toEqual([
      {
        position: 0,
        ql_query: 'priority = High',
        import_status: 'native',
        targets: [{ position: 0, target_ms: 3600000, calendar_id: 1 }],
      },
    ]);
  });
});
