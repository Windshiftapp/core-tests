import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api/core.js', () => ({ configureAPIEventHandlers: vi.fn() }));
vi.mock('../mobile/serviceWorkerClient.js', () => ({
  configureServiceWorkerNotifications: vi.fn(),
}));
vi.mock('../router.js', () => ({ navigate: vi.fn() }));
vi.mock('../stores/auth.svelte.js', () => ({
  authStore: { subscribe: vi.fn(), clearAuth: vi.fn() },
}));
vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => `t:${key}`,
  translateError: (error) => `translated:${error?.message ?? error}`,
}));
vi.mock('../utils/crossTabSync.js', () => ({ initCrossTabSync: vi.fn() }));

import { installGlobalErrorHandlers } from './appShellHandlers.js';
import { removeToast, toasts } from '../stores/toasts.svelte.js';

afterEach(() => {
  for (const toast of [...toasts.value]) removeToast(toast.id);
  vi.restoreAllMocks();
});

function rejectionEvent(reason) {
  const event = new Event('unhandledrejection');
  event.reason = reason;
  return event;
}

function errorEvent(error) {
  const event = new Event('error');
  event.error = error;
  return event;
}

describe('installGlobalErrorHandlers', () => {
  it('surfaces an unhandled rejection as an error toast', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const target = new EventTarget();
    installGlobalErrorHandlers(target);

    target.dispatchEvent(rejectionEvent(new Error('boom')));

    expect(toasts.value).toHaveLength(1);
    expect(toasts.value[0]).toMatchObject({ variant: 'error', message: 'translated:boom' });
  });

  it('ignores aborted rejections', () => {
    const target = new EventTarget();
    installGlobalErrorHandlers(target);

    const abort = new Error('aborted');
    abort.name = 'AbortError';
    target.dispatchEvent(rejectionEvent(abort));

    expect(toasts.value).toHaveLength(0);
  });

  it('ignores the runed debounce cancel sentinel leaked by onClickOutside', () => {
    // runed's onClickOutside schedules an internal useDebounce and discards its
    // promise; cancelling it on cleanup rejects with the bare string sentinel.
    const target = new EventTarget();
    installGlobalErrorHandlers(target);

    target.dispatchEvent(rejectionEvent('Cancelled'));

    expect(toasts.value).toHaveLength(0);
  });

  it('surfaces a window error event', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const target = new EventTarget();
    installGlobalErrorHandlers(target);

    target.dispatchEvent(errorEvent(new Error('sync failure')));

    expect(toasts.value).toHaveLength(1);
    expect(toasts.value[0]).toMatchObject({ message: 'translated:sync failure' });
  });

  it('ignores resource load errors that carry no error object', () => {
    const target = new EventTarget();
    installGlobalErrorHandlers(target);

    target.dispatchEvent(errorEvent(undefined));

    expect(toasts.value).toHaveLength(0);
  });

  it('installs only once per target', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const target = new EventTarget();
    installGlobalErrorHandlers(target);
    installGlobalErrorHandlers(target);

    target.dispatchEvent(rejectionEvent(new Error('boom')));

    expect(toasts.value).toHaveLength(1);
  });
});
