import { get } from 'svelte/store';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

// Mock api.notifications before importing the store.
vi.mock('../api.js', () => ({
  api: {
    notifications: {
      getAll: vi.fn(() => Promise.resolve([])),
      clearAll: vi.fn(() => Promise.resolve()),
      markAsRead: vi.fn(() => Promise.resolve()),
      markAllAsRead: vi.fn(() => Promise.resolve()),
      markItemAsRead: vi.fn(() => Promise.resolve()),
      create: vi.fn(),
    },
  },
}));

// Other side effects we don't want firing during unit tests:
vi.mock('../router.js', () => ({ navigate: vi.fn() }));
vi.mock('../utils/dateFormatter.js', () => ({
  formatDateSimple: vi.fn((d) => `formatted:${d}`),
}));
vi.mock('../utils/serverClock.js', () => ({
  serverNow: vi.fn(() => new Date('2026-05-12T12:00:00Z')),
}));
vi.mock('./activityStore.svelte.js', () => ({
  activityStore: { isIdle: false },
}));
vi.mock('./toasts.svelte.js', () => ({ addToast: vi.fn() }));

import { api } from '../api.js';
import {
  notificationActions,
  notifications,
  startNotificationPoller,
  stopNotificationPoller,
} from './notifications.js';
import { addToast } from './toasts.svelte.js';

beforeEach(async () => {
  stopNotificationPoller();
  api.notifications.getAll.mockResolvedValueOnce([]);
  await notificationActions.refresh();
  vi.clearAllMocks();
});

afterEach(() => {
  stopNotificationPoller();
  vi.restoreAllMocks();
});

describe('notification poller account lifecycle', () => {
  test('clears the old inbox and ignores a request that settles after logout', async () => {
    let resolveOldInbox;
    api.notifications.getAll.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveOldInbox = resolve;
      })
    );

    startNotificationPoller();
    stopNotificationPoller();
    resolveOldInbox([{ id: 91, title: 'Previous account', timestamp: '2026-05-12T11:00:00Z' }]);
    await Promise.resolve();
    await Promise.resolve();

    expect(get(notifications)).toEqual([]);

    api.notifications.getAll.mockResolvedValueOnce([
      { id: 92, title: 'Current account', timestamp: '2026-05-12T11:30:00Z' },
    ]);
    startNotificationPoller();
    await vi.waitFor(() => expect(get(notifications).map((item) => item.id)).toEqual([92]));
  });

  test('waits while hidden and refreshes immediately when the tab returns', async () => {
    let visibilityState = 'hidden';
    vi.spyOn(document, 'hidden', 'get').mockImplementation(() => visibilityState === 'hidden');
    vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() => visibilityState);
    api.notifications.getAll.mockResolvedValueOnce([
      {
        id: 93,
        read: false,
        type: 'mention',
        timestamp: '2026-05-12T11:45:00Z',
      },
    ]);

    startNotificationPoller();
    expect(api.notifications.getAll).not.toHaveBeenCalled();

    visibilityState = 'visible';
    document.dispatchEvent(new Event('visibilitychange'));

    await vi.waitFor(() => expect(api.notifications.getAll).toHaveBeenCalledTimes(1));
    expect(addToast).not.toHaveBeenCalled();
  });
});

describe('notificationActions.markAsRead', () => {
  test('optimistically flips read=true on the matching id', async () => {
    notifications.set([
      { id: 1, read: false, title: 'a' },
      { id: 2, read: false, title: 'b' },
    ]);

    await notificationActions.markAsRead(2);

    expect(api.notifications.markAsRead).toHaveBeenCalledWith(2);
    expect(get(notifications)).toEqual([
      { id: 1, read: false, title: 'a' },
      { id: 2, read: true, title: 'b' },
    ]);
  });

  test('leaves state untouched when the API call rejects', async () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    api.notifications.markAsRead.mockRejectedValueOnce(new Error('500'));
    notifications.set([{ id: 1, read: false }]);

    await notificationActions.markAsRead(1);

    // The implementation only updates local state on success — failure
    // path swallows the error and keeps the optimistic-free state intact.
    expect(get(notifications)).toEqual([{ id: 1, read: false }]);
    expect(errSpy).toHaveBeenCalled();
  });

  test('no-op when id does not match any notification', async () => {
    notifications.set([{ id: 1, read: false }]);
    await notificationActions.markAsRead(99);
    expect(get(notifications)).toEqual([{ id: 1, read: false }]);
  });
});

describe('notificationActions.markItemAsRead', () => {
  test('marks every notification pointing at the item read, leaves others alone', async () => {
    notifications.set([
      { id: 1, read: false, actionUrl: '/workspaces/2/items/42' },
      { id: 2, read: true, actionUrl: '/workspaces/2/items/42' }, // already read
      { id: 3, read: false, actionUrl: '/workspaces/2/items/99' }, // different item
    ]);

    await notificationActions.markItemAsRead(42);

    expect(api.notifications.markItemAsRead).toHaveBeenCalledWith(42);
    expect(get(notifications)).toEqual([
      { id: 1, read: true, actionUrl: '/workspaces/2/items/42' },
      { id: 2, read: true, actionUrl: '/workspaces/2/items/42' },
      { id: 3, read: false, actionUrl: '/workspaces/2/items/99' },
    ]);
  });

  test('leaves state untouched when the API call rejects', async () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    api.notifications.markItemAsRead.mockRejectedValueOnce(new Error('500'));
    notifications.set([{ id: 1, read: false, actionUrl: '/workspaces/2/items/42' }]);

    await notificationActions.markItemAsRead(42);

    expect(get(notifications)).toEqual([
      { id: 1, read: false, actionUrl: '/workspaces/2/items/42' },
    ]);
    expect(errSpy).toHaveBeenCalled();
  });

  test('no-op when there are no unread notifications for the item', async () => {
    notifications.set([
      { id: 1, read: true, actionUrl: '/workspaces/2/items/42' },
      { id: 2, read: false, actionUrl: '/workspaces/2/items/99' },
    ]);

    await notificationActions.markItemAsRead(42);

    expect(api.notifications.markItemAsRead).not.toHaveBeenCalled();
    expect(get(notifications)).toEqual([
      { id: 1, read: true, actionUrl: '/workspaces/2/items/42' },
      { id: 2, read: false, actionUrl: '/workspaces/2/items/99' },
    ]);
  });

  test('no-op when itemId is null/undefined', async () => {
    await notificationActions.markItemAsRead(null);
    await notificationActions.markItemAsRead(undefined);
    expect(api.notifications.markItemAsRead).not.toHaveBeenCalled();
  });
});

describe('notificationActions.dismiss', () => {
  test('removes the matching notification from local state', () => {
    notifications.set([
      { id: 1, read: false },
      { id: 2, read: false },
      { id: 3, read: true },
    ]);
    notificationActions.dismiss(2);
    expect(get(notifications)).toEqual([
      { id: 1, read: false },
      { id: 3, read: true },
    ]);
  });

  test('does not call the API (local-only dismissal)', () => {
    notifications.set([{ id: 1, read: false }]);
    notificationActions.dismiss(1);
    expect(api.notifications.markAsRead).not.toHaveBeenCalled();
  });
});

describe('notificationActions.clearAll', () => {
  test('deletes the server inbox before clearing local state', async () => {
    notifications.set([
      { id: 1, read: false },
      { id: 2, read: true },
    ]);

    await notificationActions.clearAll();

    expect(api.notifications.clearAll).toHaveBeenCalledTimes(1);
    expect(get(notifications)).toEqual([]);
  });

  test('preserves notifications and rejects when deletion fails', async () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    api.notifications.clearAll.mockRejectedValueOnce(new Error('500'));
    notifications.set([{ id: 1, read: false }]);

    await expect(notificationActions.clearAll()).rejects.toThrow('500');

    expect(get(notifications)).toEqual([{ id: 1, read: false }]);
    expect(errSpy).toHaveBeenCalled();
  });

  test('stays empty when an older inbox request settles after deletion', async () => {
    let resolveOldInbox;
    api.notifications.getAll.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveOldInbox = resolve;
      })
    );
    const refresh = notificationActions.refresh();

    const clear = notificationActions.clearAll();
    resolveOldInbox([{ id: 1, read: false, timestamp: '2026-05-12T11:00:00Z' }]);

    await Promise.all([refresh, clear]);

    expect(get(notifications)).toEqual([]);
  });
});

describe('notificationActions.markAllAsRead', () => {
  test('uses one API call and flips all unread notifications locally', async () => {
    notifications.set([
      { id: 1, read: false, title: 'a' },
      { id: 2, read: true, title: 'b' }, // already read — must not hit API
      { id: 3, read: false, title: 'c' },
    ]);

    await notificationActions.markAllAsRead();

    expect(api.notifications.markAllAsRead).toHaveBeenCalledTimes(1);
    expect(api.notifications.markAsRead).not.toHaveBeenCalled();

    // Every item now has read=true.
    expect(get(notifications).map((n) => n.read)).toEqual([true, true, true]);
  });

  test('keeps notifications unread when the API call rejects', async () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    api.notifications.markAllAsRead.mockRejectedValueOnce(new Error('500'));
    notifications.set([
      { id: 1, read: false },
      { id: 2, read: false },
    ]);

    await notificationActions.markAllAsRead();

    expect(errSpy).toHaveBeenCalled();
    expect(get(notifications).map((n) => n.read)).toEqual([false, false]);
  });

  test('does not call the API when all notifications are already read', async () => {
    notifications.set([{ id: 1, read: true }]);

    await notificationActions.markAllAsRead();

    expect(api.notifications.markAllAsRead).not.toHaveBeenCalled();
  });
});

describe('notificationActions.add', () => {
  test('prepends the created notification and maps action_url → actionUrl', async () => {
    api.notifications.create.mockResolvedValueOnce({
      id: 7,
      title: 'New',
      timestamp: '2026-05-12T10:00:00Z',
      action_url: '/items/42',
      read: false,
    });
    notifications.set([{ id: 1, title: 'existing' }]);

    const result = await notificationActions.add({ title: 'New' });

    expect(api.notifications.create).toHaveBeenCalledTimes(1);
    expect(result.actionUrl).toBe('/items/42');
    expect(result.timestamp).toBeInstanceOf(Date);

    const state = get(notifications);
    expect(state[0].id).toBe(7); // prepended
    expect(state[1].id).toBe(1);
  });

  test('rethrows on API failure and leaves state unchanged', async () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    api.notifications.create.mockRejectedValueOnce(new Error('boom'));
    notifications.set([{ id: 1 }]);

    await expect(notificationActions.add({ title: 'x' })).rejects.toThrow('boom');
    expect(get(notifications)).toEqual([{ id: 1 }]);
    expect(errSpy).toHaveBeenCalled();
  });
});

describe('notificationActions.refresh', () => {
  test('reloads notifications from the server, processes timestamps + action_url', async () => {
    api.notifications.getAll.mockResolvedValueOnce([
      {
        id: 1,
        title: 'n1',
        timestamp: '2026-05-12T11:00:00Z',
        action_url: '/x',
      },
    ]);

    await notificationActions.refresh();
    const state = get(notifications);
    expect(state).toHaveLength(1);
    expect(state[0].timestamp).toBeInstanceOf(Date);
    expect(state[0].actionUrl).toBe('/x');
  });

  test('falls back to empty array when API returns null', async () => {
    notifications.set([{ id: 99 }]);
    api.notifications.getAll.mockResolvedValueOnce(null);
    await notificationActions.refresh();
    expect(get(notifications)).toEqual([]);
  });

  test('falls back to empty array when API returns non-array', async () => {
    notifications.set([{ id: 99 }]);
    api.notifications.getAll.mockResolvedValueOnce({ unexpected: 'shape' });
    await notificationActions.refresh();
    expect(get(notifications)).toEqual([]);
  });

  test('on unexpected rejection logs and preserves the last loaded list', async () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    notifications.set([{ id: 99 }]);
    api.notifications.getAll.mockRejectedValueOnce(new Error('net'));
    await notificationActions.refresh();
    expect(get(notifications)).toEqual([{ id: 99 }]);
    expect(errSpy).toHaveBeenCalled();
  });

  test('quietly preserves the last loaded list on expected connectivity failures', async () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    notifications.set([{ id: 99 }]);
    api.notifications.getAll.mockRejectedValueOnce(
      Object.assign(new Error('offline'), { code: 'NETWORK_ERROR' })
    );

    await notificationActions.refresh();

    expect(get(notifications)).toEqual([{ id: 99 }]);
    expect(errSpy).not.toHaveBeenCalled();
  });
});

describe('notificationActions.getUnreadCount', () => {
  test('counts only unread items', () => {
    const items = [
      { id: 1, read: false },
      { id: 2, read: true },
      { id: 3, read: false },
    ];
    expect(notificationActions.getUnreadCount(items)).toBe(2);
  });

  test('returns 0 for empty list', () => {
    expect(notificationActions.getUnreadCount([])).toBe(0);
  });
});

describe('notificationActions.formatTimestamp', () => {
  // serverNow is mocked to 2026-05-12T12:00:00Z, so all asserts here are
  // relative to that fixed "now".
  test('"Just now" within 1 minute', () => {
    expect(notificationActions.formatTimestamp('2026-05-12T11:59:30Z')).toBe('Just now');
  });

  test('minute / hour / day buckets', () => {
    expect(notificationActions.formatTimestamp('2026-05-12T11:55:00Z')).toBe('5m ago');
    expect(notificationActions.formatTimestamp('2026-05-12T10:00:00Z')).toBe('2h ago');
    expect(notificationActions.formatTimestamp('2026-05-09T12:00:00Z')).toBe('3d ago');
  });

  test('falls back to formatDateSimple past 7 days', () => {
    expect(notificationActions.formatTimestamp('2026-04-01T12:00:00Z')).toBe(
      'formatted:2026-04-01T12:00:00Z'
    );
  });
});
