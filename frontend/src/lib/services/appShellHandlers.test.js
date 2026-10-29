import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiHandlers = {};
const mobileHandlers = {};
const crossTabHandlers = {};

vi.mock('../api/core.js', () => ({
  configureAPIEventHandlers: vi.fn((handlers) => Object.assign(apiHandlers, handlers)),
}));

vi.mock('../mobile/serviceWorkerClient.js', () => ({
  configureServiceWorkerNotifications: vi.fn((handlers) => Object.assign(mobileHandlers, handlers)),
}));

vi.mock('../utils/crossTabSync.js', () => ({
  initCrossTabSync: vi.fn((handlers) => Object.assign(crossTabHandlers, handlers)),
}));

vi.mock('../router.js', () => ({
  navigate: vi.fn(),
}));

vi.mock('../stores/auth.svelte.js', () => ({
  authStore: {
    clearAuth: vi.fn(),
    subscribe: vi.fn((fn) => {
      fn({ currentUser: { id: 7, is_system_admin: true } });
      return () => {};
    }),
  },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: vi.fn((key) => `translated:${key}`),
}));

vi.mock('../stores/toasts.svelte.js', () => ({
  addToast: vi.fn(),
}));

vi.mock('../stores/collectionContext.js', () => ({
  scheduleCollectionReconciliation: vi.fn(),
}));

import { scheduleCollectionReconciliation } from '../stores/collectionContext.js';
import { authStore } from '../stores/auth.svelte.js';
import { t } from '../stores/i18n.svelte.js';
import { addToast } from '../stores/toasts.svelte.js';
import { navigate } from '../router.js';
import { installAppShellHandlers, invalidateCollectionViews, notifyAdminOfClockDrift } from './appShellHandlers.js';
import { configureAPIEventHandlers } from '../api/core.js';

function setCurrentUser(user) {
  authStore.subscribe.mockImplementation((fn) => {
    fn({ currentUser: user });
    return () => {};
  });
}

describe('installAppShellHandlers', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    for (const key of Object.keys(apiHandlers)) delete apiHandlers[key];
    for (const key of Object.keys(mobileHandlers)) delete mobileHandlers[key];
    for (const key of Object.keys(crossTabHandlers)) delete crossTabHandlers[key];
    setCurrentUser({ id: 7, is_system_admin: true });
  });

  it('expires auth through the auth store when the API reports a dead session', () => {
    installAppShellHandlers();

    apiHandlers.onAuthExpired();

    expect(authStore.clearAuth).toHaveBeenCalledTimes(1);
  });

  it('wires the service-worker update notifier to a reload action toast', () => {
    installAppShellHandlers();
    const onApply = vi.fn();

    mobileHandlers.notifyUpdateAvailable({ onApply });

    expect(addToast).toHaveBeenCalledTimes(1);
    expect(addToast).toHaveBeenCalledWith(
      expect.objectContaining({
        title: 'Update available',
        variant: 'info',
        actionLabel: 'Reload',
        clickable: true,
      })
    );
    addToast.mock.calls[0][0].onClick();
    expect(onApply).toHaveBeenCalledTimes(1);
  });

  it('refreshes collection views through the cross-tab invalidation handler', async () => {
    installAppShellHandlers();

    await crossTabHandlers.onInvalidate();

    expect(scheduleCollectionReconciliation).toHaveBeenCalledTimes(1);
  });
});

describe('notifyAdminOfClockDrift', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('warns a system admin and links to the diagnostics clock panel', () => {
    setCurrentUser({ id: 7, is_system_admin: true });

    notifyAdminOfClockDrift({ offsetMs: 93_000 });

    expect(addToast).toHaveBeenCalledTimes(1);
    expect(addToast).toHaveBeenCalledWith({
      message: 'Server clock appears to be 1m 33s ahead. Click for details.',
      title: 'translated:toast.warning',
      variant: 'warning',
      clickable: true,
      onClick: expect.any(Function),
    });
    expect(t).toHaveBeenCalledWith('toast.warning');

    addToast.mock.calls[0][0].onClick();
    expect(navigate).toHaveBeenCalledWith('/admin/diagnostics?subtab=clock');
  });

  it('reports a negative offset as behind', () => {
    setCurrentUser({ id: 7, is_system_admin: true });

    notifyAdminOfClockDrift({ offsetMs: -45_000 });

    expect(addToast).toHaveBeenCalledWith(
      expect.objectContaining({ message: 'Server clock appears to be 45s behind. Click for details.' })
    );
  });

  it('stays silent for users who cannot act on the warning', () => {
    setCurrentUser({ id: 8, is_system_admin: false });

    notifyAdminOfClockDrift({ offsetMs: 93_000 });

    expect(addToast).not.toHaveBeenCalled();
    expect(navigate).not.toHaveBeenCalled();
  });
});

describe('invalidateCollectionViews', () => {
  it('schedules reconciliation on the collection context store', async () => {
    await invalidateCollectionViews();

    expect(scheduleCollectionReconciliation).toHaveBeenCalledTimes(1);
  });
});
