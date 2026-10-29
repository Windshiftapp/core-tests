import { render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api/sla.js', () => ({
  sla: {
        getAvailableCalendars: vi.fn(),
        createCalendar: vi.fn(),
        updateCalendar: vi.fn(),
        deleteCalendar: vi.fn(),
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
import SlaCalendarsTab from './SlaCalendarsTab.svelte';

const calendars = [
  { id: 1, name: 'Support hours', timezone: 'UTC', is_default: true },
  { id: 2, name: 'Team hours', timezone: 'Europe/Berlin', team_id: 7, team_name: 'Support' },
];

describe('SlaCalendarsTab', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sla.getAvailableCalendars.mockResolvedValue(calendars);
  });

  it('lists workspace-owned and team-shared calendars for the workspace', async () => {
    render(SlaCalendarsTab, { workspaceId: 5 });

    await waitFor(() => expect(sla.getAvailableCalendars).toHaveBeenCalledWith(5));

    expect(await screen.findByTestId('sla-calendar-row-1')).toBeTruthy();
    expect(screen.getByTestId('sla-calendar-row-2')).toBeTruthy();
    expect(screen.getByText('Support hours')).toBeTruthy();
    expect(screen.getByText('Team hours')).toBeTruthy();
  });

  it('loads the calendar list when the workspace changes', async () => {
    const { rerender } = render(SlaCalendarsTab, { workspaceId: 5 });
    await waitFor(() => expect(sla.getAvailableCalendars).toHaveBeenCalledWith(5));

    sla.getAvailableCalendars.mockClear();
    await rerender({ workspaceId: 9 });
    await waitFor(() => expect(sla.getAvailableCalendars).toHaveBeenCalledWith(9));
  });
});
