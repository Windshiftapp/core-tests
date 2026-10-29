import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api/sla.js', () => ({
  sla: {
        getRecalculations: vi.fn(),
        getMetrics: vi.fn(),
        startRecalculation: vi.fn(),
      },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('../../stores/toasts.svelte.js', () => ({
  successToast: vi.fn(),
  errorToast: vi.fn(),
}));

import { sla } from '../../api/sla.js';
import SlaRecalculationTab from './SlaRecalculationTab.svelte';

const jobs = [
  { id: 1, kind: 'recalc_metric', metric_id: 9, attempts: 0, state: 'pending', due_at: '2026-01-01T00:00:00Z' },
  { id: 2, kind: 'recalc_item', item_id: 42, attempts: 3, state: 'failed', due_at: '2026-01-01T00:00:00Z' },
  { id: 3, kind: 'recalc_calendar', metric_id: 10, attempts: 0, state: 'pending', due_at: '2026-01-01T00:00:00Z' },
];

describe('SlaRecalculationTab', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sla.getRecalculations.mockResolvedValue(jobs);
    sla.getMetrics.mockResolvedValue([
      { id: 9, name: 'First response' },
      { id: 10, name: 'Resolution' },
    ]);
    sla.startRecalculation.mockResolvedValue({ enqueued: 1 });
  });

  it('lists pending recalculation work', async () => {
    render(SlaRecalculationTab, { workspaceId: 5 });

    await waitFor(() => expect(sla.getRecalculations).toHaveBeenCalledWith(5));
    expect(await screen.findByTestId('sla-recalc-row-1')).toBeTruthy();
    expect(screen.getByTestId('sla-recalc-row-2')).toBeTruthy();
    expect(screen.getByTestId('sla-recalc-row-3')).toBeTruthy();
    expect(screen.getByText('First response')).toBeTruthy();
    expect(screen.getByText('Resolution')).toBeTruthy();
    expect(screen.getByText('workspaceSettings.serviceLevels.jobKinds.recalcCalendar')).toBeTruthy();
  });

  it('triggers a workspace-wide recalculation by default', async () => {
    render(SlaRecalculationTab, { workspaceId: 5 });
    await screen.findByTestId('sla-recalc-row-1');

    await fireEvent.click(screen.getByTestId('sla-recalc-trigger'));

    await waitFor(() => expect(sla.startRecalculation).toHaveBeenCalledWith(5, 0));
  });
});
