import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { removeToast, toasts } from '../stores/toasts.svelte.js';
import {
  registerMobileServiceWorker,
  resetServiceWorkerRegistrationForTests,
} from './serviceWorkerClient.js';

describe('registerMobileServiceWorker', () => {
  beforeEach(() => {
    resetServiceWorkerRegistrationForTests();
    document.head.innerHTML = '<base href="/windshift/">';
  });

  afterEach(() => {
    document.head.innerHTML = '';
    localStorage.clear();
    for (const toast of [...toasts.value]) removeToast(toast.id);
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

  it('offers a user-controlled update when a new worker is waiting', async () => {
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

    await registerMobileServiceWorker();
    await Promise.resolve();

    const updateToast = toasts.value.find((toast) => toast.title === 'Update available');
    expect(updateToast).toBeDefined();
    updateToast.onClick();
    expect(waiting.postMessage).toHaveBeenCalledWith({ type: 'SKIP_WAITING' });
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
