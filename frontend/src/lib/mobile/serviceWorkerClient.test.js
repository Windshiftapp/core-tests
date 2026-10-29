import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  clearDevelopmentServiceWorkerState,
  configureServiceWorkerNotifications,
  registerMobileServiceWorker,
  resetServiceWorkerRegistrationForTests,
} from './serviceWorkerClient.js';

function installWaitingWorker() {
  const waiting = { postMessage: vi.fn() };
  const registration = new EventTarget();
  registration.waiting = waiting;
  registration.installing = null;

  const serviceWorker = new EventTarget();
  Object.defineProperty(serviceWorker, 'controller', { value: {}, configurable: true });
  serviceWorker.register = vi.fn().mockResolvedValue(registration);
  Object.defineProperty(navigator, 'serviceWorker', {
    value: serviceWorker,
    configurable: true,
  });
  return { waiting, registration };
}

describe('registerMobileServiceWorker', () => {
  beforeEach(() => {
    resetServiceWorkerRegistrationForTests();
    document.head.innerHTML = '<base href="/windshift/">';
  });

  afterEach(() => {
    document.head.innerHTML = '';
    localStorage.clear();
    delete navigator.serviceWorker;
  });

  it('registers once using the context-path script and scope', async () => {
    const registration = { scope: '/windshift/' };
    const register = vi.fn().mockResolvedValue(registration);
    Object.defineProperty(navigator, 'serviceWorker', {
      value: { register },
      configurable: true,
    });

    const first = registerMobileServiceWorker();
    const second = registerMobileServiceWorker();

    await expect(first).resolves.toBe(registration);
    await expect(second).resolves.toBe(registration);
    expect(register).toHaveBeenCalledTimes(1);
    expect(register).toHaveBeenCalledWith('/windshift/service-worker.js', {
      scope: '/windshift/',
    });
  });

  it('offers the injected notifier a user-controlled update when a new worker is waiting', async () => {
    const { waiting } = installWaitingWorker();
    const notifyUpdateAvailable = vi.fn();
    configureServiceWorkerNotifications({ notifyUpdateAvailable });

    await registerMobileServiceWorker();
    await Promise.resolve();

    expect(notifyUpdateAvailable).toHaveBeenCalledTimes(1);
    const update = notifyUpdateAvailable.mock.calls[0][0];
    expect(typeof update.onApply).toBe('function');

    update.onApply();
    expect(waiting.postMessage).toHaveBeenCalledWith({ type: 'SKIP_WAITING' });
  });

  it('holds the update offer until the shell registers a notifier', async () => {
    installWaitingWorker();

    await registerMobileServiceWorker();
    await Promise.resolve();

    const notifyUpdateAvailable = vi.fn();
    configureServiceWorkerNotifications({ notifyUpdateAvailable });

    expect(notifyUpdateAvailable).toHaveBeenCalledTimes(1);
  });

  it('offers the update only once across repeated registration monitoring', async () => {
    installWaitingWorker();
    const notifyUpdateAvailable = vi.fn();
    configureServiceWorkerNotifications({ notifyUpdateAvailable });

    await registerMobileServiceWorker();
    await Promise.resolve();
    await Promise.resolve();

    expect(notifyUpdateAvailable).toHaveBeenCalledTimes(1);
  });

  it('allows a later retry after registration fails', async () => {
    const register = vi
      .fn()
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce({ scope: '/windshift/' });
    Object.defineProperty(navigator, 'serviceWorker', {
      value: { register },
      configurable: true,
    });

    await expect(registerMobileServiceWorker()).resolves.toBeNull();
    await expect(registerMobileServiceWorker()).resolves.toEqual({ scope: '/windshift/' });
    expect(register).toHaveBeenCalledTimes(2);
  });
});

describe('clearDevelopmentServiceWorkerState', () => {
  afterEach(() => {
    delete globalThis.caches;
  });

  it('unregisters every installed worker and drops their caches', async () => {
    const unregisterFirst = vi.fn().mockResolvedValue(true);
    const unregisterSecond = vi.fn().mockResolvedValue(true);
    Object.defineProperty(navigator, 'serviceWorker', {
      value: {
        getRegistrations: vi
          .fn()
          .mockResolvedValue([{ unregister: unregisterFirst }, { unregister: unregisterSecond }]),
      },
      configurable: true,
    });
    const deleteCache = vi.fn().mockResolvedValue(true);
    globalThis.caches = {
      keys: vi.fn().mockResolvedValue(['windshift-v1', 'windshift-v2']),
      delete: deleteCache,
    };

    await expect(clearDevelopmentServiceWorkerState()).resolves.toBe(2);

    expect(unregisterFirst).toHaveBeenCalledOnce();
    expect(unregisterSecond).toHaveBeenCalledOnce();
    expect(deleteCache).toHaveBeenCalledWith('windshift-v1');
    expect(deleteCache).toHaveBeenCalledWith('windshift-v2');
  });

  it('is a no-op where service workers are unavailable', async () => {
    delete navigator.serviceWorker;

    await expect(clearDevelopmentServiceWorkerState()).resolves.toBe(0);
  });

  it('still clears caches when a worker refuses to unregister', async () => {
    Object.defineProperty(navigator, 'serviceWorker', {
      value: {
        getRegistrations: vi
          .fn()
          .mockResolvedValue([{ unregister: vi.fn().mockRejectedValue(new Error('locked')) }]),
      },
      configurable: true,
    });
    const deleteCache = vi.fn().mockResolvedValue(true);
    globalThis.caches = { keys: vi.fn().mockResolvedValue(['windshift-v1']), delete: deleteCache };

    await expect(clearDevelopmentServiceWorkerState()).resolves.toBe(1);

    expect(deleteCache).toHaveBeenCalledWith('windshift-v1');
  });
});
