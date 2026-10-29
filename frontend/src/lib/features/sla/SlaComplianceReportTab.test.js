import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api/sla.js', () => ({
  sla: { getReport: vi.fn() },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('../../utils/dateFormatter.js', () => ({
  formatDate: (value) => String(value),
}));

import { sla } from '../../api/sla.js';
import SlaComplianceReportTab from './SlaComplianceReportTab.svelte';

const report = {
  from: '2026-01-01T00:00:00Z',
  to: '2026-02-01T00:00:00Z',
  metrics: [
    {
      metric_id: 1,
      metric_name: 'First response',
      ongoing: 3,
      currently_breached: 1,
      completed: 10,
      breached: 2,
      avg_elapsed_ms: 1800000,
      avg_goal_ms: 3600000,
      max_elapsed_ms: 7200000,
    },
  ],
  coverage: {
    reference: 'team_service_hours',
    team_ids: [2],
    sla_counted_ms: 2592000000,
    team_service_ms: 864000000,
    uncovered_ms: 1728000000,
    excluded_ms: 0,
    breached_outside_service_hours: 1,
    current_state_reference: true,
  },
  breached_items: [
    {
      cycle_id: 99,
      item_id: 7,
      item_key: 'ABC-7',
      title: 'Late ticket',
      metric_name: 'First response',
      stopped_at: '2026-01-15T09:00:00Z',
      breached_at: '2026-01-15T08:00:00Z',
      elapsed_ms: 7200000,
      goal_duration_ms: 3600000,
    },
  ],
};

describe('SlaComplianceReportTab', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sla.getReport.mockResolvedValue(report);
  });

  it('renders per-metric aggregates and the breached-item list', async () => {
    render(SlaComplianceReportTab, { workspaceId: 5 });

    await waitFor(() => expect(sla.getReport).toHaveBeenCalledWith(5, { from: '', to: '' }));
    expect(await screen.findByTestId('sla-report-metric-1')).toBeTruthy();
    expect(screen.getAllByText('First response').length).toBeGreaterThanOrEqual(1);
    expect(screen.getByTestId('sla-report-coverage')).toBeTruthy();
    expect(screen.getByTestId('sla-report-breach-99')).toBeTruthy();
    expect(screen.getByText('ABC-7')).toBeTruthy();
  });

  it('reloads the report for the selected date range', async () => {
    render(SlaComplianceReportTab, { workspaceId: 5 });
    await screen.findByTestId('sla-report-metric-1');

    await fireEvent.input(screen.getByTestId('sla-report-from'), {
      target: { value: '2026-01-01' },
    });
    await fireEvent.input(screen.getByTestId('sla-report-to'), {
      target: { value: '2026-02-01' },
    });
    await fireEvent.click(screen.getByTestId('sla-report-apply'));

    await waitFor(() =>
      expect(sla.getReport).toHaveBeenLastCalledWith(5, {
        from: '2026-01-01',
        to: '2026-02-01',
      })
    );
  });
});
