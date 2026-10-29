import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api/sla.js', () => ({
  sla: {
        getTeamCalendars: vi.fn(),
        createTeamCalendar: vi.fn(),
        updateTeamCalendar: vi.fn(),
        deleteTeamCalendar: vi.fn(),
        getTeamCalendarImpact: vi.fn(),
      },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('../stores/toasts.svelte.js', () => ({
  successToast: vi.fn(),
  errorToast: vi.fn(),
}));

vi.mock('../composables/useConfirm.js', () => ({
  confirm: vi.fn().mockResolvedValue(false),
}));

import { sla } from '../api/sla.js';
import ServiceHoursTab from './ServiceHoursTab.svelte';

describe('ServiceHoursTab', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sla.getTeamCalendars.mockResolvedValue([
      { id: 3, name: 'Support hours', timezone: 'UTC', is_default: true },
    ]);
    sla.getTeamCalendarImpact.mockResolvedValue({
      calendar_id: 3,
      workspaces: [
        {
          workspace_id: 7,
          workspace_name: 'Support workspace',
          ongoing_cycles: 2,
          goal_targets: [{ target_id: 1, metric_name: 'First response', target_ms: 3600000 }],
        },
      ],
    });
  });

  it('lists the team calendars', async () => {
    render(ServiceHoursTab, { team: { id: 2 }, canEdit: true });

    await waitFor(() => expect(sla.getTeamCalendars).toHaveBeenCalledWith(2));
    expect(await screen.findByTestId('team-calendar-row-3')).toBeTruthy();
    expect(screen.getByText('Support hours')).toBeTruthy();
  });

  it('previews the edit impact across bound workspaces', async () => {
    render(ServiceHoursTab, { team: { id: 2 }, canEdit: true });
    await screen.findByTestId('team-calendar-row-3');

    // Row actions render behind the trigger; call the action directly through
    // the impact button when present.
    const trigger = screen.getByTestId('team-calendar-actions-3');
    await fireEvent.click(trigger);
    const impactAction = await screen.findByTestId('team-calendar-impact-3');
    await fireEvent.click(impactAction);

    await waitFor(() => expect(sla.getTeamCalendarImpact).toHaveBeenCalledWith(2, 3));
    expect(await screen.findByTestId('team-calendar-impact-workspace-7')).toBeTruthy();
    expect(screen.getByText('Support workspace')).toBeTruthy();
  });
});
