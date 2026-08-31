import { fireEvent, render, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('../../api/diagnostics.js', () => ({
  getRecurrenceVolume: vi.fn(),
  updateRecurrenceVolumeSettings: vi.fn(),
}));

vi.mock('../../stores/toasts.svelte.js', () => ({
  successToast: vi.fn(),
  errorToast: vi.fn(),
}));

import { getRecurrenceVolume, updateRecurrenceVolumeSettings } from '../../api/diagnostics.js';
import { successToast } from '../../stores/toasts.svelte.js';
import RecurrenceVolumeSection from './RecurrenceVolumeSection.svelte';

describe('RecurrenceVolumeSection', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    getRecurrenceVolume.mockResolvedValue({
      diagnostic_enabled: true,
      warning_threshold: 80,
      hard_limit: 100,
      scheduler_batch_size: 100,
      total_rules: 85,
      active_rules: 84,
      due_rules: 12,
      batch_backlogged: false,
      healthy: false,
      workspaces: [
        {
          workspace_id: 7,
          key: 'OPS',
          name: 'Operations',
          rule_count: 85,
          active_count: 84,
          warning: true,
          at_capacity: false,
        },
      ],
    });
    updateRecurrenceVolumeSettings.mockResolvedValue({
      diagnostic_enabled: false,
      warning_threshold: 70,
    });
  });

  test('shows volume warnings and saves administrator-controlled settings', async () => {
    const view = render(RecurrenceVolumeSection);

    await waitFor(() => {
      expect(view.getByTestId('recurrence-volume-alert')).toBeTruthy();
    });
    expect(view.getByText('OPS — Operations')).toBeTruthy();
    expect(view.getByText('Warning')).toBeTruthy();

    await fireEvent.click(view.getByTestId('recurrence-volume-enabled'));
    const threshold = view.container.querySelector('#recurrence-volume-threshold');
    await fireEvent.input(threshold, { target: { value: '70' } });
    await fireEvent.click(view.getByTestId('recurrence-volume-save'));

    await waitFor(() => {
      expect(updateRecurrenceVolumeSettings).toHaveBeenCalledWith({
        diagnostic_enabled: false,
        warning_threshold: 70,
      });
    });
    expect(successToast).toHaveBeenCalledWith('Recurrence diagnostic settings saved');
  });
});
