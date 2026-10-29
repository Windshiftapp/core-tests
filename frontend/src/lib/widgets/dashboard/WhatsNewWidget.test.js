import { fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  watchedItems: [],
  markAsRead: vi.fn(),
  navigate: vi.fn(),
  openItem: vi.fn(),
}));

vi.mock('../../stores/homepageStore.svelte.js', async () => {
  const { writable } = await import('svelte/store');
  return {
    homepageStore: {
          get watchedItems() {
            return mocks.watchedItems;
          },
          loading: false,
          formatRelativeTime: () => '1 day ago',
        },
  };
})

vi.mock('../../stores/workspaces.svelte.js', async () => {
  const { writable } = await import('svelte/store');
  return {
    workspacesStore: writable({
          allWorkspaces: [
            { id: 1, name: 'Windshift' },
            { id: 2, name: 'Marketing' },
          ],
        }),
  };
});

vi.mock('../../stores/notifications.js', async () => {
  const { writable } = await import('svelte/store');
  return {
    notifications: writable([]),
    notificationActions: { markAsRead: mocks.markAsRead },
  };
});

vi.mock('../../router.js', () => ({ navigate: mocks.navigate }));

vi.mock('../../stores/itemModalStore.svelte.js', () => ({
  itemModalStore: { open: mocks.openItem },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({ t: (key) => key }));

vi.mock('@lucide/svelte', () => ({ Inbox: function MockInbox() {} }));

const { notifications } = await import('../../stores/notifications.js');
const { default: WhatsNewWidget } = await import('./WhatsNewWidget.svelte');

const NOW = new Date('2026-10-13T12:00:00Z');
const daysAgo = (days) => new Date(NOW.getTime() - days * 24 * 60 * 60 * 1000).toISOString();

beforeEach(() => {
  // Fake only Date so Testing Library's async waits still use real timers.
  vi.useFakeTimers({ toFake: ['Date'] });
  vi.setSystemTime(NOW);
  vi.clearAllMocks();
  mocks.watchedItems.length = 0;
  notifications.set([]);
});

afterEach(() => {
  vi.useRealTimers();
});

describe("What's New widget", () => {
  it('renders watched items updated within the last two weeks as compact work item rows', async () => {
    mocks.watchedItems.push({
      item_id: 7,
      workspace_id: 1,
      workspace_key: 'WI',
      workspace_item_number: 7,
      title: 'Post-fix smoke test',
      status: 'Open',
      status_builtin_key: 'open',
      status_color: '#3b82f6',
      item_type_name: 'Bug',
      item_type_icon: 'Bug',
      item_type_color: '#ef4444',
      priority_name: 'High',
      priority_builtin_key: 'high',
      priority_color: '#ef4444',
      last_active_at: daysAgo(5),
    });

    render(WhatsNewWidget);

    const rows = await screen.findAllByTestId('whats-new-entry');
    expect(rows).toHaveLength(1);
    expect(rows[0].classList.contains('dashboard-item-row')).toBe(true);
    expect(rows[0]).toHaveAttribute('data-item-key', 'WI-7');
    expect(rows[0]).toHaveTextContent('Post-fix smoke test');
    expect(rows[0]).toHaveTextContent('WI-7');
    expect(rows[0].querySelector('.dashboard-item-status')).not.toBeNull();
    expect(rows[0].querySelector('[data-testid="dashboard-item-type-icon"]')).not.toBeNull();

    await fireEvent.click(rows[0]);
    expect(mocks.openItem).toHaveBeenCalledWith(7, 1);
    expect(mocks.navigate).not.toHaveBeenCalled();
    expect(mocks.markAsRead).not.toHaveBeenCalled();
  });

  it('hides watched items whose last activity is older than two weeks', async () => {
    mocks.watchedItems.push({
      item_id: 7,
      workspace_id: 1,
      workspace_key: 'WI',
      workspace_item_number: 7,
      title: 'Stale watched item',
      status: 'Open',
      last_active_at: daysAgo(15),
    });

    render(WhatsNewWidget);

    expect(await screen.findByText('dashboard.allCaughtUp')).toBeInTheDocument();
    expect(screen.queryAllByTestId('whats-new-entry')).toHaveLength(0);
  });

  it('renders notifications within the last two weeks and marks them read on click', async () => {
    notifications.set([
      {
        id: 42,
        message: 'Sam mentioned you in WI-7',
        timestamp: new Date(daysAgo(3)),
        read: false,
        actionUrl: '/workspaces/1/items/7',
      },
    ]);

    render(WhatsNewWidget);

    const rows = await screen.findAllByTestId('whats-new-entry');
    expect(rows).toHaveLength(1);
    expect(rows[0].classList.contains('dashboard-item-row')).toBe(true);
    expect(rows[0]).toHaveTextContent('Sam mentioned you in WI-7');

    await fireEvent.click(rows[0]);
    expect(mocks.markAsRead).toHaveBeenCalledWith(42);
    expect(mocks.openItem).toHaveBeenCalledWith(7, 1);
    expect(mocks.navigate).not.toHaveBeenCalled();
  });

  it('hides unread notifications older than two weeks', async () => {
    notifications.set([
      {
        id: 42,
        message: 'Month-old unread notification',
        timestamp: new Date(daysAgo(30)),
        read: false,
        actionUrl: '/workspaces/2/items/9',
      },
    ]);

    render(WhatsNewWidget);

    expect(await screen.findByText('dashboard.allCaughtUp')).toBeInTheDocument();
    expect(screen.queryAllByTestId('whats-new-entry')).toHaveLength(0);
  });
});
