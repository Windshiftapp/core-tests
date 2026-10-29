import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api/sla.js', () => ({
  sla: {
        getCoveragePreview: vi.fn(),
        getTeamCalendarImpact: vi.fn(),
        getCalendarImpact: vi.fn(),
      },
}));

vi.mock('../../utils/timeUtils.js', () => ({
  listIanaTimezones: () => ['UTC', 'Europe/Berlin'],
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  i18n: { locale: 'en' },
  t: (key, params) => (params ? `${key} ${JSON.stringify(params)}` : key),
}));

import { sla } from '../../api/sla.js';
import WorkingCalendarEditor from './WorkingCalendarEditor.svelte';

const workspaceCalendar = {
  id: 3,
  workspace_id: 5,
  name: 'Support hours',
  timezone: 'UTC',
  weekly_intervals: { monday: [{ start: '09:00', end: '17:00' }] },
  holidays: [],
};

describe('WorkingCalendarEditor coverage preview', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sla.getTeamCalendarImpact.mockResolvedValue({ calendar_id: 3, workspaces: [] });
    sla.getCalendarImpact.mockResolvedValue({ calendar_id: 3, workspaces: [] });
    sla.getCoveragePreview.mockResolvedValue({ reference: 'none' });
  });

  it('shows the coverage comparison for a workspace-owned calendar', async () => {
    sla.getCoveragePreview.mockResolvedValue({
      reference: 'team_service_hours',
      sla_weekly_ms: 604800000,
      team_weekly_ms: 172800000,
      overlap_weekly_ms: 172800000,
      discrepancy: 'sla_wider',
    });

    render(WorkingCalendarEditor, {
      isOpen: true,
      calendar: workspaceCalendar,
      workspaceId: 5,
      onSave: vi.fn(),
    });

    await waitFor(() => expect(sla.getCoveragePreview).toHaveBeenCalledWith(5, 3));
    expect(await screen.findByTestId('sla-calendar-coverage')).toBeTruthy();
  });

  it('does not request coverage for a team-owned calendar', async () => {
    render(WorkingCalendarEditor, {
      isOpen: true,
      calendar: { ...workspaceCalendar, workspace_id: null, team_id: 7 },
      workspaceId: 5,
      onSave: vi.fn(),
    });

    await Promise.resolve();
    expect(sla.getCoveragePreview).not.toHaveBeenCalled();
  });

  it('shows the recalculation scope for a team-owned calendar', async () => {
    sla.getTeamCalendarImpact.mockResolvedValue({
      calendar_id: 3,
      workspaces: [
        { workspace_id: 5, workspace_name: 'Support', ongoing_cycles: 4 },
        { workspace_id: 6, workspace_name: 'Sales', ongoing_cycles: 2 },
      ],
    });

    render(WorkingCalendarEditor, {
      isOpen: true,
      calendar: { ...workspaceCalendar, workspace_id: null, team_id: 7 },
      workspaceId: 5,
      onSave: vi.fn(),
    });

    await waitFor(() => expect(sla.getTeamCalendarImpact).toHaveBeenCalledWith(7, 3));
    const impact = await screen.findByTestId('sla-calendar-ongoing-impact');
    expect(impact.textContent).toContain('"cycles":6');
    expect(impact.textContent).toContain('"workspaces":2');
  });

  it('shows the recalculation scope for a workspace-owned calendar', async () => {
    sla.getCalendarImpact.mockResolvedValue({
      calendar_id: 3,
      workspaces: [{ workspace_id: 5, workspace_name: 'Support', ongoing_cycles: 3 }],
    });

    render(WorkingCalendarEditor, {
      isOpen: true,
      calendar: workspaceCalendar,
      workspaceId: 5,
      onSave: vi.fn(),
    });

    await waitFor(() => expect(sla.getCalendarImpact).toHaveBeenCalledWith(5, 3));
    const impact = await screen.findByTestId('sla-calendar-ongoing-impact');
    expect(impact.textContent).toContain('"cycles":3');
    expect(impact.textContent).toContain('"workspaces":1');
  });

  it('loads team recalculation scope without a workspace context', async () => {
    sla.getTeamCalendarImpact.mockResolvedValue({
      calendar_id: 3,
      workspaces: [{ workspace_id: 5, workspace_name: 'Support', ongoing_cycles: 2 }],
    });

    render(WorkingCalendarEditor, {
      isOpen: true,
      calendar: { ...workspaceCalendar, workspace_id: null, team_id: 7 },
      workspaceId: null,
      onSave: vi.fn(),
    });

    await waitFor(() => expect(sla.getTeamCalendarImpact).toHaveBeenCalledWith(7, 3));
    const impact = await screen.findByTestId('sla-calendar-ongoing-impact');
    expect(impact.textContent).toContain('"cycles":2');
  });

  it('reports when a team calendar has no bound workspace referencing it', async () => {
    sla.getTeamCalendarImpact.mockResolvedValue({ calendar_id: 3, workspaces: [] });

    render(WorkingCalendarEditor, {
      isOpen: true,
      calendar: { ...workspaceCalendar, workspace_id: null, team_id: 7 },
      workspaceId: 5,
      onSave: vi.fn(),
    });

    const impact = await screen.findByTestId('sla-calendar-ongoing-impact');
    expect(impact.textContent).toContain('applyToOngoingNoImpact');
  });

  it('renders the timezone options with their zone labels', async () => {
    render(WorkingCalendarEditor, {
      isOpen: true,
      calendar: null,
      workspaceId: 5,
      onSave: vi.fn(),
    });

    const input = document.getElementById('sla-calendar-timezone');
    await fireEvent.click(input);

    const listbox = await screen.findByTestId('sla-calendar-timezone-listbox');
    expect(within(listbox).getByText(/Europe\/Berlin/)).toBeTruthy();
    expect(within(listbox).getAllByTestId('sla-calendar-timezone-option')).toHaveLength(2);
  });

  it('associates the timezone label with the picker control', async () => {
    render(WorkingCalendarEditor, {
      isOpen: true,
      calendar: null,
      workspaceId: 5,
      onSave: vi.fn(),
    });

    const input = document.getElementById('sla-calendar-timezone');
    const label = document.querySelector('label[for="sla-calendar-timezone"]');
    expect(label).toBeTruthy();
    expect(label.control).toBe(input);
  });
});

describe('WorkingCalendarEditor save actions', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sla.getTeamCalendarImpact.mockResolvedValue({ calendar_id: 3, workspaces: [] });
    sla.getCalendarImpact.mockResolvedValue({ calendar_id: 3, workspaces: [] });
    sla.getCoveragePreview.mockResolvedValue({ reference: 'none' });
  });

  it('saves without recalculating ongoing cycles from the primary action', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(WorkingCalendarEditor, {
      isOpen: true,
      calendar: workspaceCalendar,
      workspaceId: 5,
      onSave,
    });

    await fireEvent.click(screen.getByTestId('sla-calendar-submit'));

    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    expect(onSave.mock.calls[0][0]).toMatchObject({
      apply_to_ongoing: false,
      timezone: 'UTC',
    });
  });

  it('saves and recalculates ongoing cycles from the secondary action', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(WorkingCalendarEditor, {
      isOpen: true,
      calendar: workspaceCalendar,
      workspaceId: 5,
      onSave,
    });

    await fireEvent.click(screen.getByTestId('sla-calendar-submit-recalculate'));

    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    expect(onSave.mock.calls[0][0]).toMatchObject({ apply_to_ongoing: true });
  });

  it('does not offer recalculation while creating a calendar', async () => {
    render(WorkingCalendarEditor, {
      isOpen: true,
      calendar: null,
      workspaceId: 5,
      onSave: vi.fn(),
    });

    expect(screen.queryByTestId('sla-calendar-submit-recalculate')).toBeNull();
    expect(screen.getByTestId('sla-calendar-submit')).toBeTruthy();
  });
});
