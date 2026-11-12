import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('./i18n.svelte.js', () => ({
  t: (key) => `t:${key}`,
  translateError: (error) => `translated:${error?.message ?? error}`,
}));

import { removeToast, surfaceError, toasts } from './toasts.svelte.js';

afterEach(() => {
  for (const toast of [...toasts.value]) removeToast(toast.id);
  vi.restoreAllMocks();
});

describe('surfaceError', () => {
  it('logs the error with its context and adds an error toast', () => {
    const logged = vi.spyOn(console, 'error').mockImplementation(() => {});
    const error = new Error('save failed');

    surfaceError(error, { context: 'saveThing' });

    expect(logged).toHaveBeenCalledWith('[saveThing]', error);
    expect(toasts.value).toHaveLength(1);
    expect(toasts.value[0]).toMatchObject({
      variant: 'error',
      message: 'translated:save failed',
      title: 't:toast.error',
    });
  });

  it('logs without a toast when silent', () => {
    const logged = vi.spyOn(console, 'error').mockImplementation(() => {});

    surfaceError(new Error('background refresh failed'), { silent: true });

    expect(logged).toHaveBeenCalledTimes(1);
    expect(toasts.value).toHaveLength(0);
  });

  it('renders a thrown string directly', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});

    surfaceError('plain failure');

    expect(toasts.value[0]).toMatchObject({ message: 'plain failure' });
  });

  it('lets the caller override the message and title', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});

    surfaceError(new Error('raw'), { message: 'Could not save', title: 'Save failed' });

    expect(toasts.value[0]).toMatchObject({ message: 'Could not save', title: 'Save failed' });
  });

  it('adds a warning toast when asked', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});

    surfaceError(new Error('degraded'), { level: 'warning' });

    expect(toasts.value[0]).toMatchObject({ variant: 'warning' });
  });

  it('ignores a caller-cancelled request', () => {
    const logged = vi.spyOn(console, 'error').mockImplementation(() => {});

    surfaceError(new DOMException('The request was canceled', 'AbortError'), {
      context: 'loadAfterClose',
    });

    expect(toasts.value).toHaveLength(0);
    expect(logged).not.toHaveBeenCalled();
  });

  it('ignores a REQUEST_CANCELED error that bypassed the API mapping', () => {
    const logged = vi.spyOn(console, 'error').mockImplementation(() => {});

    surfaceError({ code: 'REQUEST_CANCELED', message: 'Request was canceled' });

    expect(toasts.value).toHaveLength(0);
    expect(logged).not.toHaveBeenCalled();
  });
});
