import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api/sla.js', () => ({
  sla: {
        getWarningThresholds: vi.fn(),
        getMetrics: vi.fn(),
        createWarningThreshold: vi.fn(),
        updateWarningThreshold: vi.fn(),
        deleteWarningThreshold: vi.fn(),
      },
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
import SlaWarningThresholdsTab from './SlaWarningThresholdsTab.svelte';

const thresholds = [
  { id: 1, label: 'Approaching', percent: 75, metric_id: null, is_active: true },
  { id: 2, label: 'Metric only', percent: 50, metric_id: 9, metric_name: 'First response', is_active: false },
];

describe('SlaWarningThresholdsTab', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sla.getWarningThresholds.mockResolvedValue(thresholds);
    sla.getMetrics.mockResolvedValue([{ id: 9, name: 'First response' }]);
  });

  it('lists workspace-scoped and metric-scoped thresholds', async () => {
    render(SlaWarningThresholdsTab, { workspaceId: 5 });

    await waitFor(() => expect(sla.getWarningThresholds).toHaveBeenCalledWith(5));
    expect(await screen.findByTestId('sla-threshold-row-1')).toBeTruthy();
    expect(screen.getByTestId('sla-threshold-row-2')).toBeTruthy();
    expect(screen.getByText('Approaching')).toBeTruthy();
    expect(screen.getByText('First response')).toBeTruthy();
  });

  it('rejects a percent outside 1-99 before calling the API', async () => {
    render(SlaWarningThresholdsTab, { workspaceId: 5 });
    await screen.findByTestId('sla-threshold-row-1');

    await fireEvent.click(screen.getByTestId('sla-threshold-add'));
    await fireEvent.input(screen.getByTestId('sla-threshold-percent'), {
      target: { value: '150' },
    });
    await fireEvent.click(screen.getByTestId('sla-threshold-submit'));

    expect(sla.createWarningThreshold).not.toHaveBeenCalled();
    expect(await screen.findByTestId('sla-threshold-error')).toBeTruthy();
  });
});
