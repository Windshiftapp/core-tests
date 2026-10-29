import { cleanup, fireEvent, render, screen, within } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('../../api/agentRuns.js', () => ({
  agentRuns: {
    listForItem: vi.fn(),
  },
}));

vi.mock('../../api/items.js', () => ({
  items: {
        getStatusDurations: vi.fn(),
      },
}));

vi.mock('../../stores/workItemStalenessSettings.svelte.js', () => ({
  workItemStalenessSettings: {
      staleAfterDays: 30,
    },
}))

vi.mock('../../stores/itemDetailStore.svelte.js', () => ({
  itemDetailStore: {
      includeChildItems: false,
      timeRollup: null,
      timeRollupLoading: false,
      loadTimeRollup: vi.fn(),
    },
}))

vi.mock('../../stores/workspaceDataStore.svelte.js', () => ({
  workspaceDataStore: {
      statuses: [],
    },
}));

vi.mock('../../utils/serverClock.js', () => ({
  serverNow: vi.fn(() => new Date('2026-08-20T12:00:00Z')),
}));

const translations = {
  'items.activity': 'Activity',
  'items.healthOverview': 'Item health',
  'items.healthOverviewDescription': 'Computed item signals',
  'items.activityHealthCompleted': 'Complete',
  'items.activityHealthUnknown': 'Unknown',
  'items.activityHealthStale': 'Stale',
  'items.activityHealthActive': 'Active',
  'items.activityMonitoringComplete': 'Activity monitoring ended',
  'items.activityToday': 'Active today',
  'items.lastActivityAt': 'Last activity {date}',
  'items.activityUnavailable': 'No activity timestamp available',
  'items.dueDate': 'Due date',
  'items.dueHealthCompleted': 'Complete',
  'items.dueHealthUnscheduled': 'Not scheduled',
  'items.dueHealthOverdue': 'Overdue',
  'items.dueHealthToday': 'Due today',
  'items.dueHealthSoon': 'Due soon',
  'items.dueHealthScheduled': 'Scheduled',
  'items.workCompleted': 'Work completed',
  'items.completedOn': 'Completed {date}',
  'items.dueOn': 'Due {date}',
  'items.dueDateUnavailable': 'No due date is set',
  'items.timeInStatus': 'Time in status',
  'items.unknown': 'Unknown',
  'items.statusSince': 'In this status since {date}',
  'items.statusHistoryUnavailable': 'Status timing is unavailable',
  'items.statusDurationsDescription': 'Total time in each status',
  'items.statusDurationsLoading': 'Calculating status durations…',
  'items.statusDurationsLoadError': 'Status durations could not be calculated.',
  'items.statusDurationsRetry': 'Try again',
  'items.statusDurationsEmpty': 'No status history is available yet.',
  'items.currentStatus': 'Current',
  'items.durationLessThanMinute': '< 1m',
  'items.timeline': 'Timeline',
  'items.created': 'Created',
  'items.lastUpdated': 'Last Updated',
  'items.workItemInformation': 'Work Item Information',
  'items.id': 'ID',
  'items.type': 'Type',
  'items.parent': 'Parent',
  'items.workItem': 'Work Item',
  'items.by': 'by',
  'dueDate.noDueDate': 'No due date',
  'dueDate.dueToday': 'Due today',
  'dueDate.dueTomorrow': 'Due tomorrow',
  'dueDate.dueYesterday': 'Due yesterday',
  'dueDate.dueInDays': 'Due in {days} days',
  'dueDate.overdueByDays': 'Overdue by {days} days',
};

function translate(key, params = {}) {
  if (key === 'items.activityIdleDays') {
    return `${params.count} day${params.count === 1 ? '' : 's'} idle`;
  }
  let value = translations[key] ?? key;
  for (const [name, replacement] of Object.entries(params)) {
    value = value.replace(`{${name}}`, String(replacement));
  }
  return value;
}

vi.mock('../../stores/i18n.svelte.js', () => ({
  i18n: { locale: 'en-US' },
  t: translate,
}));

vi.mock('../../utils/authenticatedDateFormatter.js', () => ({
  formatAuthenticatedDateTime: (value) => value || '',
  formatAuthenticatedInstant: (value) => {
    const date = new Date(value);
    return `${String(date.getUTCHours()).padStart(2, '0')}:${String(date.getUTCMinutes()).padStart(2, '0')}`;
  },
}));

import { agentRuns } from '../../api/agentRuns.js';
import { items } from '../../api/items.js';
import { itemDetailStore } from '../../stores/itemDetailStore.svelte.js';
import { workItemStalenessSettings } from '../../stores/workItemStalenessSettings.svelte.js';
import { workspaceDataStore } from '../../stores/workspaceDataStore.svelte.js';
import ItemDetailTabs from './ItemDetailTabs.svelte';

const baseItem = {
  id: 987,
  workspace_id: 3,
  workspace_item_number: 1259,
  parent_id: 900,
  parent_workspace_item_number: 1100,
  item_type_name: 'Task',
  status_id: 2,
  status_name: 'In Progress',
  created_at: '2026-08-01T09:00:00Z',
  updated_at: '2026-08-05T11:00:00Z',
  last_active_at: '2026-08-05T11:00:00Z',
  status_since: '2026-08-06T12:00:00Z',
  due_date: '2026-08-25T00:00:00Z',
};

function renderDetails(item = baseItem) {
  return render(ItemDetailTabs, {
    props: {
      item,
      workspace: { id: 3, key: 'WIND' },
      tab: 'details',
      moduleSettings: { time_tracking_enabled: true },
    },
  });
}

function renderTime(worklogs, item = baseItem) {
  return render(ItemDetailTabs, {
    props: {
      item,
      workspace: { id: 3, key: 'WIND' },
      tab: 'time',
      moduleSettings: { time_tracking_enabled: true },
      timeWorklogs: worklogs,
    },
  });
}

beforeEach(() => {
  agentRuns.listForItem.mockResolvedValue([]);
  items.getStatusDurations.mockResolvedValue({
    statuses: [
      { status_id: 1, status_name: 'Open', duration_seconds: 432000, is_current: false },
      { status_id: 2, status_name: 'In Progress', duration_seconds: 1209600, is_current: true },
    ],
  });
  workItemStalenessSettings.staleAfterDays = 30;
  workspaceDataStore.statuses = [{ id: 2, is_completed: false }];
  itemDetailStore.includeChildItems = false;
  itemDetailStore.timeRollup = null;
  itemDetailStore.timeRollupLoading = false;
});

describe('ItemDetailTabs complete worklog totals', () => {
  test.each([
    [50, '6d 2h'],
    [51, '6d 3h'],
    [201, '25d 1h'],
  ])('renders all %i entries and their exact total', (count, expectedTotal) => {
    const worklogs = Array.from({ length: count }, (_, index) => ({
      id: index + 1,
      duration_minutes: 60,
      date: 1_788_537_600,
    }));

    renderTime(worklogs);

    expect(screen.getByText(`items.timeEntries (${count})`)).toBeInTheDocument();
    expect(screen.getByText(`${expectedTotal} items.logged`)).toBeInTheDocument();
  });

  test('continues to use the server rollup when child items are included', () => {
    itemDetailStore.includeChildItems = true;
    itemDetailStore.timeRollup = {
      total_logged_minutes: 960,
      total_estimate_minutes: 0,
      item_count: 4,
      truncated: false,
    };

    renderTime([{ id: 1, duration_minutes: 60, date: 1_788_537_600 }]);

    expect(screen.getByText('2d items.logged')).toBeInTheDocument();
    expect(screen.queryByText('1h items.logged')).not.toBeInTheDocument();
  });
});

describe('ItemDetailTabs worklog table layout', () => {
  const worklog = {
    id: 1,
    description: 'A very long worklog description that used to wrap across many lines in the table',
    user_name: 'Stefan',
    duration_minutes: 120,
    start_time: Date.UTC(2026, 9, 11, 12, 0, 0) / 1000,
    end_time: Date.UTC(2026, 9, 11, 14, 0, 0) / 1000,
    project_name: 'Windshift Development',
  };

  test('drops the start, end, and project columns', () => {
    renderTime([worklog]);

    expect(screen.queryByTestId('table-column-start_time')).not.toBeInTheDocument();
    expect(screen.queryByTestId('table-column-end_time')).not.toBeInTheDocument();
    expect(screen.queryByTestId('table-column-project_name')).not.toBeInTheDocument();
  });

  test('clamps the description to two lines', () => {
    renderTime([worklog]);

    expect(screen.getByText(worklog.description)).toHaveClass('line-clamp-2');
  });

  test('reveals the start and end times as a tooltip over the duration', async () => {
    renderTime([worklog]);

    const duration = screen.getByText('2h 0m');
    await fireEvent.focus(duration.parentElement);

    const tooltip = await screen.findByTestId('tooltip');
    expect(tooltip).toHaveTextContent('12:00 — 14:00');
  });
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('ItemDetailTabs details health', () => {
  test('shows stale activity, an approaching due date, and all status durations', async () => {
    renderDetails({
      ...baseItem,
      created_at: '2026-07-01T09:00:00Z',
      updated_at: '2026-07-19T11:00:00Z',
      last_active_at: '2026-07-19T11:00:00Z',
    });

    const activity = screen.getByTestId('item-health-activity');
    expect(within(activity).getByText('Stale')).toBeInTheDocument();
    expect(within(activity).getByText('32 days idle')).toBeInTheDocument();

    const dueDate = screen.getByTestId('item-health-due-date');
    expect(within(dueDate).queryByText('Due soon')).not.toBeInTheDocument();
    expect(within(dueDate).getByText('Due in 5 days')).toBeInTheDocument();
    expect(within(dueDate).getByText('Due Aug 25, 2026')).toBeInTheDocument();

    const open = await screen.findByTestId('item-status-duration-1');
    expect(within(open).getByText('Open')).toBeInTheDocument();
    expect(within(open).getByText('5d')).toBeInTheDocument();
    const inProgress = screen.getByTestId('item-status-duration-2');
    expect(within(inProgress).getByText('In Progress')).toBeInTheDocument();
    expect(within(inProgress).getByText('2w')).toBeInTheDocument();
    expect(within(inProgress).getByText('Current')).toBeInTheDocument();

    expect(screen.getByText('WIND-1259')).toBeInTheDocument();
    expect(screen.getByText('WIND-1100')).toBeInTheDocument();
  });

  test('does not flag completed work as stale or overdue', () => {
    workspaceDataStore.statuses = [{ id: 2, is_completed: true }];
    renderDetails({
      ...baseItem,
      last_active_at: '2026-07-01T12:00:00Z',
      due_date: '2026-08-01T00:00:00Z',
      status_name: 'Done',
    });

    const activity = screen.getByTestId('item-health-activity');
    expect(within(activity).getByText('Complete')).toBeInTheDocument();
    expect(within(activity).queryByText('Stale')).not.toBeInTheDocument();

    const dueDate = screen.getByTestId('item-health-due-date');
    expect(within(dueDate).getByText('Work completed')).toBeInTheDocument();
    expect(within(dueDate).queryByText('Complete')).not.toBeInTheDocument();
    expect(within(dueDate).queryByText('Overdue')).not.toBeInTheDocument();
  });

  test('keeps the threshold boundaries explicit', () => {
    renderDetails({
      ...baseItem,
      last_active_at: '2026-08-07T12:00:00Z',
      due_date: '2026-08-28T00:00:00Z',
    });

    const activity = screen.getByTestId('item-health-activity');
    expect(within(activity).getByText('13 days idle')).toBeInTheDocument();
    expect(within(activity).queryByText('Active')).not.toBeInTheDocument();

    const dueDate = screen.getByTestId('item-health-due-date');
    expect(within(dueDate).getByText('Aug 28')).toBeInTheDocument();
    expect(within(dueDate).queryByText('Scheduled')).not.toBeInTheDocument();
    expect(within(dueDate).queryByText('Due soon')).not.toBeInTheDocument();
  });

  test('uses the configured work item staleness threshold', () => {
    workItemStalenessSettings.staleAfterDays = 60;
    renderDetails({
      ...baseItem,
      created_at: '2026-06-01T12:00:00Z',
      updated_at: '2026-07-01T12:00:00Z',
      last_active_at: '2026-07-01T12:00:00Z',
    });

    const activity = screen.getByTestId('item-health-activity');
    expect(within(activity).getByText('50 days idle')).toBeInTheDocument();
    expect(within(activity).queryByText('Active')).not.toBeInTheDocument();
  });

  test('omits labels and descriptions that repeat the displayed values', () => {
    renderDetails({
      ...baseItem,
      created_at: '2026-08-20T09:00:00Z',
      updated_at: '2026-08-20T11:00:00Z',
      last_active_at: '2026-08-20T11:00:00Z',
      due_date: null,
    });

    const activity = screen.getByTestId('item-health-activity');
    expect(within(activity).getByText('Active today')).toBeInTheDocument();
    expect(within(activity).queryByText('Active')).not.toBeInTheDocument();

    const dueDate = screen.getByTestId('item-health-due-date');
    expect(within(dueDate).getByText('No due date')).toBeInTheDocument();
    expect(within(dueDate).queryByText('Not scheduled')).not.toBeInTheDocument();
    expect(within(dueDate).queryByText('No due date is set')).not.toBeInTheDocument();

    expect(screen.queryByText('Item health')).not.toBeInTheDocument();
    expect(screen.queryByText('Computed item signals')).not.toBeInTheDocument();
    expect(screen.queryByText('Total time in each status')).not.toBeInTheDocument();
  });

  test('uses creation as the activity baseline when last activity is a zero timestamp', () => {
    renderDetails({
      ...baseItem,
      created_at: '2026-07-19T12:00:00Z',
      updated_at: '2026-07-19T12:00:00Z',
      last_active_at: '0001-01-01T00:00:00Z',
    });

    const activity = screen.getByTestId('item-health-activity');
    expect(within(activity).getByText('32 days idle')).toBeInTheDocument();
    expect(within(activity).getByText('Last activity 2026-07-19T12:00:00Z')).toBeInTheDocument();
  });

  test('lets the user retry a failed status-duration calculation', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    items.getStatusDurations
      .mockRejectedValueOnce(new Error('temporary failure'))
      .mockResolvedValueOnce({
        statuses: [
          { status_id: 2, status_name: 'In Progress', duration_seconds: 3600, is_current: true },
        ],
      });
    renderDetails();

    await screen.findByTestId('item-status-durations-error');
    await fireEvent.click(screen.getByTestId('item-status-durations-retry'));

    const current = await screen.findByTestId('item-status-duration-2');
    expect(within(current).getByText('1h')).toBeInTheDocument();
    expect(items.getStatusDurations).toHaveBeenCalledTimes(2);
  });
});
