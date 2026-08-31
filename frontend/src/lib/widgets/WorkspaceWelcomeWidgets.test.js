import { render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  getItems: vi.fn(),
  getIterations: vi.fn(),
  getMilestones: vi.fn(),
  getStatuses: vi.fn(),
}));

vi.mock('../api.js', () => ({
  api: {
    items: { getAll: mocks.getItems },
    iterations: { getAll: mocks.getIterations },
    milestones: { getAll: mocks.getMilestones },
    statuses: { getAll: mocks.getStatuses },
  },
}));

vi.mock('../stores', async () => {
  const { writable } = await import('svelte/store');
  return {
    authStore: writable({ currentUser: { id: 42 } }),
  };
});

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('@lucide/svelte', () => ({
  AlertCircle: function MockAlertCircle() {},
  Calendar: function MockCalendar() {},
  CalendarDays: function MockCalendarDays() {},
  CheckSquare: function MockCheckSquare() {},
  Flag: function MockFlag() {},
  Loader2: function MockLoader() {},
  RefreshCw: function MockRefresh() {},
}));

vi.mock('./dashboard/DueMark.svelte', () => ({
  default: function MockDueMark() {},
}));

import MyTasksWidget from './MyTasksWidget.svelte';
import OverdueItemsWidget from './OverdueItemsWidget.svelte';
import UpcomingDeadlinesWidget from './UpcomingDeadlinesWidget.svelte';

beforeEach(() => {
  mocks.getItems.mockResolvedValue({ items: [] });
  mocks.getIterations.mockResolvedValue([]);
  mocks.getMilestones.mockResolvedValue([]);
  mocks.getStatuses.mockResolvedValue([{ id: 9, category_name: 'Closed', is_completed: true }]);
});

describe('workspace dashboard item filters', () => {
  it('requests only incomplete items for My Tasks and renders the result', async () => {
    mocks.getItems.mockResolvedValue({
      items: [
        {
          id: 21,
          title: 'Prepare 0.8.7',
          workspace_id: 7,
          workspace_item_number: 21,
          workspace_key: 'WI',
        },
      ],
    });

    render(MyTasksWidget, { workspaceId: 7, maxItems: 8 });

    expect(await screen.findByText('Prepare 0.8.7')).toBeInTheDocument();
    expect(mocks.getItems).toHaveBeenCalledWith({
      ql: 'workspace_id = 7 AND assignee_id = 42 AND status_completed = false',
      limit: 24,
      order_by: 'created_at',
    });
  });

  it('uses category completion semantics for overdue items', async () => {
    render(OverdueItemsWidget, { workspaceId: 7 });

    await waitFor(() => expect(mocks.getItems).toHaveBeenCalledOnce());
    expect(mocks.getItems).toHaveBeenCalledWith({
      ql: 'workspace_id = 7 AND due_date < now() AND status_completed = false',
      limit: 50,
    });
    expect(mocks.getStatuses).not.toHaveBeenCalled();
    expect(screen.getByText('widgets.overdueItems.emptyTitle')).toBeInTheDocument();
  });

  it('uses category completion semantics for upcoming deadlines', async () => {
    render(UpcomingDeadlinesWidget, { workspaceId: 7 });

    await waitFor(() => expect(mocks.getItems).toHaveBeenCalledOnce());
    expect(mocks.getItems).toHaveBeenCalledWith({
      ql: 'workspace_id = 7 AND due_date >= now() AND status_completed = false',
      limit: 50,
    });
    expect(mocks.getStatuses).not.toHaveBeenCalled();
    expect(screen.getByText('widgets.upcomingDeadlines.emptyTitle')).toBeInTheDocument();
  });
});
