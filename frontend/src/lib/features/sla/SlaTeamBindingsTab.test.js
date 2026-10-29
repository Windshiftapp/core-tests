import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api/sla.js', () => ({
  sla: {
        getWorkspaceTeamBindings: vi.fn(),
        getAvailableCalendars: vi.fn(),
        createWorkspaceTeamBinding: vi.fn(),
        deleteWorkspaceTeamBinding: vi.fn(),
      },
}))

vi.mock('../../api/teams.js', () => ({
  teams: { getAll: vi.fn() },
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
import { teams } from '../../api/teams.js';
import SlaTeamBindingsTab from './SlaTeamBindingsTab.svelte';

describe('SlaTeamBindingsTab', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sla.getWorkspaceTeamBindings.mockResolvedValue([
      { id: 1, team_id: 2, team_name: 'Support' },
    ]);
    sla.getAvailableCalendars.mockResolvedValue([
      { id: 10, team_id: 2, name: 'Support hours' },
      { id: 11, team_id: 2, name: 'Escalation hours' },
    ]);
    teams.getAll.mockResolvedValue([{ id: 2, name: 'Support' }, { id: 3, name: 'Platform' }]);
  });

  it('lists bound teams and how many of their calendars are available', async () => {
    render(SlaTeamBindingsTab, { workspaceId: 5 });

    await waitFor(() => expect(sla.getWorkspaceTeamBindings).toHaveBeenCalledWith(5));
    expect(await screen.findByTestId('sla-binding-row-1')).toBeTruthy();
    expect(screen.getByText('Support')).toBeTruthy();
    expect(screen.getByText('2')).toBeTruthy();
  });

  it('requires a team before creating a binding', async () => {
    render(SlaTeamBindingsTab, { workspaceId: 5 });
    await screen.findByTestId('sla-binding-row-1');

    await fireEvent.click(screen.getByTestId('sla-binding-add'));
    await fireEvent.click(screen.getByTestId('sla-binding-submit'));

    expect(sla.createWorkspaceTeamBinding).not.toHaveBeenCalled();
    expect(await screen.findByTestId('sla-binding-error')).toBeTruthy();
  });
});
