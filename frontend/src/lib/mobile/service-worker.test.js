import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import vm from 'node:vm';
import { afterEach, describe, expect, it, vi } from 'vitest';

const workerSource = readFileSync(resolve(process.cwd(), 'public/service-worker.js'), 'utf8');

function createWorker(scope = 'https://windshift.test/windshift/') {
  const handlers = {};
  const stored = new Map();
  const cache = {
    put: vi.fn(async (key, response) => stored.set(key, response.clone())),
    match: vi.fn(async (key) => stored.get(key)?.clone()),
  };
  const caches = {
    open: vi.fn(async () => cache),
    keys: vi.fn(async () => []),
    delete: vi.fn(async () => true),
  };
  const registration = {
    scope,
    showNotification: vi.fn(async () => {}),
  };
  const clients = {
    claim: vi.fn(async () => {}),
    matchAll: vi.fn(async () => []),
    openWindow: vi.fn(async () => {}),
  };
  const self = {
    registration,
    clients,
    skipWaiting: vi.fn(async () => {}),
    addEventListener: vi.fn((type, handler) => {
      handlers[type] = handler;
    }),
  };
  const fetch = vi.fn();

  vm.runInNewContext(workerSource, {
    self,
    caches,
    fetch,
    Response,
    URL,
    AbortController,
    setTimeout,
    clearTimeout,
    Promise,
    console,
  });

  return { handlers, stored, cache, caches, registration, clients, self, fetch };
}

function dispatchExtendable(handler, fields = {}) {
  let pending;
  handler({
    ...fields,
    waitUntil(promise) {
      pending = promise;
    },
  });
  return pending;
}

function dispatchFetch(worker, request = { mode: 'navigate' }, fields = {}) {
  let response;
  worker.handlers.fetch({
    ...fields,
    request,
    respondWith(promise) {
      response = promise;
    },
  });
  return response;
}

afterEach(() => {
  vi.useRealTimers();
});

describe('PWA service worker', () => {
  it('installs a dependency-free recovery document', async () => {
    const worker = createWorker();

    await dispatchExtendable(worker.handlers.install);

    expect(worker.self.skipWaiting).not.toHaveBeenCalled();
    expect(worker.cache.put).toHaveBeenCalledTimes(1);
    const cached = worker.stored.get('recovery-document');
    expect(cached.status).toBe(503);
    await expect(cached.text()).resolves.toContain("Windshift couldn't connect");
  });

  it('lets the client apply a waiting update on demand', () => {
    const worker = createWorker();

    worker.handlers.message({ data: { type: 'SKIP_WAITING' } });

    expect(worker.self.skipWaiting).toHaveBeenCalledTimes(1);
  });

  it('ignores unrelated client messages', () => {
    const worker = createWorker();

    worker.handlers.message({ data: { type: 'OTHER_MESSAGE' } });

    expect(worker.self.skipWaiting).not.toHaveBeenCalled();
  });

  it('returns recovery UI after an offline navigation failure', async () => {
    const worker = createWorker();
    worker.fetch.mockRejectedValue(new TypeError('offline'));

    const response = await dispatchFetch(worker);

    expect(response.status).toBe(503);
    await expect(response.text()).resolves.toContain('Retry');
  });

  it('returns recovery UI for retryable server failures without caching them', async () => {
    const worker = createWorker();
    worker.fetch.mockResolvedValue(new Response('gateway', { status: 503 }));

    const response = await dispatchFetch(worker);

    expect(response.status).toBe(503);
    await expect(response.text()).resolves.toContain('Connection problem');
    expect(worker.cache.put).not.toHaveBeenCalled();
  });

  it('passes successful navigation responses through unchanged', async () => {
    const worker = createWorker();
    const online = new Response('<main>online</main>', {
      status: 200,
      headers: { 'Content-Type': 'text/html' },
    });
    worker.fetch.mockResolvedValue(online);

    const response = await dispatchFetch(worker);

    expect(response).toBe(online);
    expect(worker.cache.put).not.toHaveBeenCalled();
  });

  it('aborts a black-holed navigation at the deadline', async () => {
    vi.useFakeTimers();
    const worker = createWorker();
    worker.fetch.mockImplementation(
      (_request, options) =>
        new Promise((_resolve, reject) => {
          options.signal.addEventListener('abort', () => reject(new Error('aborted')));
        })
    );

    const responsePromise = dispatchFetch(worker);
    await vi.advanceTimersByTimeAsync(10_000);
    const response = await responsePromise;

    expect(response.status).toBe(503);
    expect(worker.fetch.mock.calls[0][1].signal.aborted).toBe(true);
  });

  it('applies the navigation deadline while preload is still pending', async () => {
    vi.useFakeTimers();
    const worker = createWorker();
    const responsePromise = dispatchFetch(
      worker,
      { mode: 'navigate' },
      {
        preloadResponse: new Promise(() => {}),
      }
    );
    let settled = false;
    responsePromise.then(() => {
      settled = true;
    });

    await vi.advanceTimersByTimeAsync(10_000);

    expect(settled).toBe(true);
    const response = await responsePromise;
    expect(response.status).toBe(503);
    expect(worker.fetch).not.toHaveBeenCalled();
  });

  it('enables navigation preload when the browser supports it', async () => {
    const worker = createWorker();
    worker.registration.navigationPreload = { enable: vi.fn(async () => {}) };

    await dispatchExtendable(worker.handlers.activate);

    expect(worker.registration.navigationPreload.enable).toHaveBeenCalledTimes(1);
  });

  it('deletes only obsolete Windshift PWA caches during activation', async () => {
    const worker = createWorker();
    worker.caches.keys.mockResolvedValue([
      'windshift-shell-v1',
      'windshift-pwa-windshift-v1',
      'windshift-pwa-windshift-v2',
      'windshift-pwa-another-context-v1',
      'plugin-cache-v1',
    ]);

    await dispatchExtendable(worker.handlers.activate);

    expect(worker.caches.delete).toHaveBeenCalledTimes(2);
    expect(worker.caches.delete).toHaveBeenCalledWith('windshift-shell-v1');
    expect(worker.caches.delete).toHaveBeenCalledWith('windshift-pwa-windshift-v1');
    expect(worker.caches.delete).not.toHaveBeenCalledWith('windshift-pwa-another-context-v1');
    expect(worker.caches.delete).not.toHaveBeenCalledWith('plugin-cache-v1');
  });

  it('resolves push deep links and icons against the registration scope', async () => {
    const worker = createWorker('https://windshift.test/team-a/');

    await dispatchExtendable(worker.handlers.push, {
      data: {
        json: () => ({ title: 'Assigned', url: '/m/items/42' }),
      },
    });

    expect(worker.registration.showNotification).toHaveBeenCalledWith(
      'Assigned',
      expect.objectContaining({
        data: { url: 'https://windshift.test/team-a/m/items/42' },
        icon: 'https://windshift.test/team-a/apple-touch-icon.png',
        actions: [{ action: 'open', title: 'Open Windshift' }],
      })
    );
  });
});
