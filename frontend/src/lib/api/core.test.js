import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

// Stub serverClock — fetchAPI calls updateOffset(headers.Date) on every
// response. Without the stub the real clock-sample logic runs and pulls
// extra dependencies.
vi.mock('../utils/serverClock.js', () => ({
  updateOffset: vi.fn(),
  getClockOffset: vi.fn(() => 0),
  getSampleCount: vi.fn(() => 0),
  isClockDriftSignificant: vi.fn(() => false),
}));

// The 401 branch in fetchAPI dynamically imports '../stores' and calls
// authStore.clearAuth(). Stub it so we can assert the side effect without
// loading the entire stores barrel. The mock factory is hoisted, so the
// clearAuth spy lives inside it — referenced via the mocked module below.
vi.mock('../stores', () => ({
  authStore: { clearAuth: vi.fn(), subscribe: vi.fn() },
}));

import { authStore } from '../stores';
import {
  ADMIN_UI_MUTATION_EVENT,
  clearAPIRequestSessionKey,
  fetchAPI,
  setAPIRequestSessionKey,
} from './core.js';

// Build a Response-like mock without depending on the actual Response
// constructor (jsdom's varies subtly across versions).
function makeResponse({ status = 200, statusText = 'OK', body = '', headers = {} }) {
  const h = new Map(Object.entries(headers));
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText,
    headers: { get: (k) => h.get(k.toLowerCase()) ?? h.get(k) ?? null },
    text: vi.fn(() => Promise.resolve(body)),
    json: vi.fn(() => Promise.resolve(JSON.parse(body || '{}'))),
  };
}

beforeEach(() => {
  clearAPIRequestSessionKey();
  vi.clearAllMocks();
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe('fetchAPI — happy path', () => {
  test('returns parsed JSON body for 200 with JSON content-type', async () => {
    global.fetch = vi.fn(() =>
      Promise.resolve(
        makeResponse({
          status: 200,
          body: '{"id":42,"name":"alpha"}',
          headers: { 'content-type': 'application/json' },
        })
      )
    );

    const result = await fetchAPI('/items/42');
    expect(result).toEqual({ id: 42, name: 'alpha' });
    expect(global.fetch).toHaveBeenCalledWith(
      '/api/items/42',
      expect.objectContaining({
        credentials: 'same-origin',
        headers: expect.objectContaining({ 'Content-Type': 'application/json' }),
      })
    );
  });

  test('returns null for 204 No Content', async () => {
    global.fetch = vi.fn(() =>
      Promise.resolve(makeResponse({ status: 204, statusText: 'No Content' }))
    );
    await expect(fetchAPI('/items/42', { method: 'DELETE' })).resolves.toBeNull();
  });

  test('returns null when content-type is not JSON', async () => {
    global.fetch = vi.fn(() =>
      Promise.resolve(
        makeResponse({
          status: 200,
          body: '<html>hi</html>',
          headers: { 'content-type': 'text/html' },
        })
      )
    );
    await expect(fetchAPI('/static/banner')).resolves.toBeNull();
  });
});

describe('fetchAPI — error mapping', () => {
  test('parses structured JSON error envelopes', async () => {
    global.fetch = vi.fn(() =>
      Promise.resolve(
        makeResponse({
          status: 400,
          statusText: 'Bad Request',
          body: JSON.stringify({
            error: 'Title is required',
            code: 'VALIDATION_FAILED',
            details: { field: 'title' },
            request_id: 'req-abc',
          }),
        })
      )
    );

    let caught;
    try {
      await fetchAPI('/items', { method: 'POST', body: '{}' });
    } catch (e) {
      caught = e;
    }

    expect(caught).toBeInstanceOf(Error);
    expect(caught.message).toBe('Title is required');
    expect(caught.code).toBe('VALIDATION_FAILED');
    expect(caught.errorCode).toBe('VALIDATION_FAILED'); // alias
    expect(caught.details).toEqual({ field: 'title' });
    expect(caught.requestId).toBe('req-abc');
    expect(caught.status).toBe(400);
    expect(caught.statusText).toBe('Bad Request');
  });

  test("falls back to 'message' field when 'error' is missing", async () => {
    global.fetch = vi.fn(() =>
      Promise.resolve(
        makeResponse({
          status: 422,
          statusText: 'Unprocessable',
          body: JSON.stringify({ message: 'Validation failed', code: 'VAL' }),
        })
      )
    );

    let caught;
    try {
      await fetchAPI('/x');
    } catch (e) {
      caught = e;
    }
    expect(caught.message).toBe('Validation failed');
    expect(caught.code).toBe('VAL');
  });

  test('non-JSON body becomes the error message verbatim', async () => {
    global.fetch = vi.fn(() =>
      Promise.resolve(
        makeResponse({
          status: 500,
          statusText: 'Internal Server Error',
          body: 'something broke',
        })
      )
    );
    let caught;
    try {
      await fetchAPI('/x');
    } catch (e) {
      caught = e;
    }
    expect(caught.message).toBe('something broke');
    expect(caught.status).toBe(500);
    expect(caught.code).toBeUndefined();
  });

  test('empty 502 produces the dedicated gateway-timeout message', async () => {
    global.fetch = vi.fn(() =>
      Promise.resolve(makeResponse({ status: 502, statusText: 'Bad Gateway', body: '' }))
    );
    let caught;
    try {
      await fetchAPI('/x');
    } catch (e) {
      caught = e;
    }
    expect(caught.message).toBe('The server took too long to respond. Please try again shortly.');
  });

  test('empty 504 produces the dedicated gateway-timeout message', async () => {
    global.fetch = vi.fn(() =>
      Promise.resolve(makeResponse({ status: 504, statusText: 'Gateway Timeout', body: '' }))
    );
    let caught;
    try {
      await fetchAPI('/x');
    } catch (e) {
      caught = e;
    }
    expect(caught.message).toBe('The server took too long to respond. Please try again shortly.');
  });

  test('501 with empty body uses statusText fallback (not the 502/504 message)', async () => {
    global.fetch = vi.fn(() =>
      Promise.resolve(makeResponse({ status: 501, statusText: 'Not Implemented', body: '' }))
    );
    let caught;
    try {
      await fetchAPI('/x');
    } catch (e) {
      caught = e;
    }
    expect(caught.message).toBe('Request failed: Not Implemented');
  });
});

describe('fetchAPI — 401 logout side effect', () => {
  test('calls authStore.clearAuth() when the server returns 401', async () => {
    global.fetch = vi.fn(() =>
      Promise.resolve(
        makeResponse({
          status: 401,
          statusText: 'Unauthorized',
          body: JSON.stringify({ error: 'no session' }),
        })
      )
    );

    await expect(fetchAPI('/items')).rejects.toThrow('no session');
    expect(authStore.clearAuth).toHaveBeenCalledTimes(1);
  });

  test('does not call clearAuth on non-401 errors', async () => {
    global.fetch = vi.fn(() =>
      Promise.resolve(
        makeResponse({
          status: 403,
          body: JSON.stringify({ error: 'forbidden' }),
        })
      )
    );
    await expect(fetchAPI('/items')).rejects.toThrow('forbidden');
    expect(authStore.clearAuth).not.toHaveBeenCalled();
  });

  test('preserves a pending-auth session and exposes policy response fields', async () => {
    global.fetch = vi.fn(() =>
      Promise.resolve(
        makeResponse({
          status: 403,
          body: JSON.stringify({
            error: 'Passkey required',
            code: 'AUTHENTICATION_PENDING',
            passkey_required: true,
            policy_message: 'Use your passkey',
          }),
        })
      )
    );

    let caught;
    try {
      await fetchAPI('/items');
    } catch (error) {
      caught = error;
    }
    expect(caught.passkey_required).toBe(true);
    expect(caught.policy_message).toBe('Use your passkey');
    expect(caught.body.passkey_required).toBe(true);
    expect(authStore.clearAuth).not.toHaveBeenCalled();
  });

  test('does not clear auth for a legacy 401 pending-auth response', async () => {
    global.fetch = vi.fn(() =>
      Promise.resolve(
        makeResponse({
          status: 401,
          body: JSON.stringify({
            error: 'Passkey enrollment required',
            code: 'AUTHENTICATION_PENDING',
          }),
        })
      )
    );

    await expect(fetchAPI('/items')).rejects.toThrow('Passkey enrollment required');
    expect(authStore.clearAuth).not.toHaveBeenCalled();
  });
});

describe('fetchAPI — network and timeout errors', () => {
  test('TypeError from fetch surfaces as a NETWORK_ERROR with helpful copy', async () => {
    global.fetch = vi.fn(() => Promise.reject(new TypeError('Failed to fetch')));

    let caught;
    try {
      await fetchAPI('/items');
    } catch (e) {
      caught = e;
    }
    expect(caught).toBeInstanceOf(Error);
    expect(caught.status).toBe(0);
    expect(caught.code).toBe('NETWORK_ERROR');
    expect(caught.message).toMatch(/check your connection/i);
  });

  test('aborts a request at its explicit deadline and reports REQUEST_TIMEOUT', async () => {
    vi.useFakeTimers();
    global.fetch = vi.fn(
      (_url, options) =>
        new Promise((_resolve, reject) => {
          options.signal.addEventListener('abort', () =>
            reject(new DOMException('Aborted', 'AbortError'))
          );
        })
    );

    const request = expect(fetchAPI('/bootstrap', { timeout: 250 })).rejects.toMatchObject({
      code: 'REQUEST_TIMEOUT',
      status: 0,
    });
    await vi.advanceTimersByTimeAsync(250);

    await request;
  });

  test('preserves caller-driven AbortError instead of mapping it to a network failure', async () => {
    const controller = new AbortController();
    global.fetch = vi.fn(
      (_url, options) =>
        new Promise((_resolve, reject) => {
          options.signal.addEventListener('abort', () =>
            reject(new DOMException('Aborted', 'AbortError'))
          );
        })
    );

    const request = fetchAPI('/items/42', { signal: controller.signal });
    controller.abort();

    await expect(request).rejects.toMatchObject({ name: 'AbortError' });
  });

  test('treats a hidden-document TypeError as navigation cancellation', async () => {
    const originalVisibilityState = document.visibilityState;
    Object.defineProperty(document, 'visibilityState', {
      configurable: true,
      value: 'hidden',
    });
    global.fetch = vi.fn(() => Promise.reject(new TypeError('Failed to fetch')));

    try {
      await expect(fetchAPI('/items')).rejects.toMatchObject({ name: 'AbortError' });
    } finally {
      Object.defineProperty(document, 'visibilityState', {
        configurable: true,
        value: originalVisibilityState,
      });
    }
  });

  test('treats a pagehide TypeError as navigation cancellation', async () => {
    window.dispatchEvent(new Event('pagehide'));
    global.fetch = vi.fn(() => Promise.reject(new TypeError('Failed to fetch')));

    try {
      await expect(fetchAPI('/items')).rejects.toMatchObject({ name: 'AbortError' });
    } finally {
      window.dispatchEvent(new Event('pageshow'));
    }
  });
});

describe('fetchAPI — request shape', () => {
  test('threads options.headers and body through to fetch', async () => {
    global.fetch = vi.fn(() =>
      Promise.resolve(
        makeResponse({
          status: 200,
          body: '{}',
          headers: { 'content-type': 'application/json' },
        })
      )
    );

    await fetchAPI('/items', {
      method: 'POST',
      headers: { 'X-Custom': 'yes' },
      body: '{"x":1}',
    });

    expect(global.fetch).toHaveBeenCalledWith(
      '/api/items',
      expect.objectContaining({
        method: 'POST',
        body: '{"x":1}',
        headers: expect.objectContaining({
          'Content-Type': 'application/json',
          'X-Custom': 'yes',
        }),
      })
    );
  });
});

describe('fetchAPI — administration UI refresh signaling', () => {
  test('successful mutations from an admin page emit one shell refresh signal', async () => {
    const events = [];
    const onMutation = (event) => events.push(event.detail);
    window.addEventListener(ADMIN_UI_MUTATION_EVENT, onMutation);
    window.history.replaceState({}, '', '/admin/llm-connections');
    global.fetch = vi.fn(() =>
      Promise.resolve(
        makeResponse({
          status: 200,
          body: '{"id":42}',
          headers: { 'content-type': 'application/json' },
        })
      )
    );

    try {
      await fetchAPI('/admin/llm-connections', {
        method: 'POST',
        body: '{"name":"main"}',
      });
    } finally {
      window.removeEventListener(ADMIN_UI_MUTATION_EVENT, onMutation);
      window.history.replaceState({}, '', '/');
    }

    expect(events).toEqual([{ endpoint: '/admin/llm-connections', method: 'POST' }]);
  });

  test('supports admin routes below a configured context path', async () => {
    const onMutation = vi.fn();
    window.__WINDSHIFT_CONTEXT_PATH__ = '/windshift';
    window.history.replaceState({}, '', '/windshift/admin/modules');
    window.addEventListener(ADMIN_UI_MUTATION_EVENT, onMutation);
    global.fetch = vi.fn(() =>
      Promise.resolve(makeResponse({ status: 204, statusText: 'No Content' }))
    );

    try {
      await fetchAPI('/setup/modules', { method: 'PUT', body: '{}' });
    } finally {
      window.removeEventListener(ADMIN_UI_MUTATION_EVENT, onMutation);
      delete window.__WINDSHIFT_CONTEXT_PATH__;
      window.history.replaceState({}, '', '/');
    }

    expect(onMutation).toHaveBeenCalledTimes(1);
  });

  test('does not emit for reads, failed mutations, or mutations outside admin', async () => {
    const onMutation = vi.fn();
    window.addEventListener(ADMIN_UI_MUTATION_EVENT, onMutation);
    global.fetch = vi
      .fn()
      .mockResolvedValueOnce(
        makeResponse({
          status: 200,
          body: '{}',
          headers: { 'content-type': 'application/json' },
        })
      )
      .mockResolvedValueOnce(
        makeResponse({
          status: 400,
          statusText: 'Bad Request',
          body: '{"error":"invalid"}',
        })
      )
      .mockResolvedValueOnce(makeResponse({ status: 204, statusText: 'No Content' }));

    try {
      window.history.replaceState({}, '', '/admin/modules');
      await fetchAPI('/setup/modules');
      await expect(fetchAPI('/setup/modules', { method: 'PUT', body: '{}' })).rejects.toThrow(
        'invalid'
      );
      window.history.replaceState({}, '', '/workspaces/1');
      await fetchAPI('/items/1', { method: 'DELETE' });
    } finally {
      window.removeEventListener(ADMIN_UI_MUTATION_EVENT, onMutation);
      window.history.replaceState({}, '', '/');
    }

    expect(onMutation).not.toHaveBeenCalled();
  });
});

describe('fetchAPI — authenticated in-flight GET ownership', () => {
  test('coalesces concurrent normalized GET paths within one session', async () => {
    let resolveFetch;
    global.fetch = vi.fn(
      () =>
        new Promise((resolve) => {
          resolveFetch = resolve;
        })
    );
    setAPIRequestSessionKey('session-a');

    const first = fetchAPI('/catalog?b=2&a=1');
    const second = fetchAPI('/catalog?a=1&b=2');

    expect(first).toBe(second);
    expect(global.fetch).toHaveBeenCalledTimes(1);

    resolveFetch(
      makeResponse({
        body: '{"items":[1]}',
        headers: { 'content-type': 'application/json' },
      })
    );
    await expect(Promise.all([first, second])).resolves.toEqual([{ items: [1] }, { items: [1] }]);

    global.fetch.mockResolvedValueOnce(
      makeResponse({ body: '{"items":[2]}', headers: { 'content-type': 'application/json' } })
    );
    await expect(fetchAPI('/catalog?a=1&b=2')).resolves.toEqual({ items: [2] });
    expect(global.fetch).toHaveBeenCalledTimes(2);
  });

  test('does not coalesce unauthenticated requests', async () => {
    global.fetch = vi.fn(() =>
      Promise.resolve(makeResponse({ body: '{}', headers: { 'content-type': 'application/json' } }))
    );

    await Promise.all([fetchAPI('/catalog'), fetchAPI('/catalog')]);

    expect(global.fetch).toHaveBeenCalledTimes(2);
  });

  test('does not share a pending request across session changes', async () => {
    const resolvers = [];
    global.fetch = vi.fn(
      () =>
        new Promise((resolve) => {
          resolvers.push(resolve);
        })
    );

    setAPIRequestSessionKey('session-a');
    const first = fetchAPI('/catalog');
    setAPIRequestSessionKey('session-b');
    const second = fetchAPI('/catalog');

    expect(global.fetch).toHaveBeenCalledTimes(2);
    resolvers[0](
      makeResponse({ body: '{"session":"a"}', headers: { 'content-type': 'application/json' } })
    );
    resolvers[1](
      makeResponse({ body: '{"session":"b"}', headers: { 'content-type': 'application/json' } })
    );
    await expect(first).resolves.toEqual({ session: 'a' });
    await expect(second).resolves.toEqual({ session: 'b' });
  });

  test('removes failed requests so retries reach the network', async () => {
    setAPIRequestSessionKey('session-a');
    global.fetch = vi
      .fn()
      .mockRejectedValueOnce(new TypeError('offline'))
      .mockResolvedValueOnce(
        makeResponse({ body: '{"ok":true}', headers: { 'content-type': 'application/json' } })
      );

    await expect(fetchAPI('/catalog')).rejects.toMatchObject({ code: 'NETWORK_ERROR' });
    await expect(fetchAPI('/catalog')).resolves.toEqual({ ok: true });
    expect(global.fetch).toHaveBeenCalledTimes(2);
  });

  test('keeps mutations and caller-controlled GET lifetimes independent', async () => {
    setAPIRequestSessionKey('session-a');
    global.fetch = vi.fn(() =>
      Promise.resolve(makeResponse({ body: '{}', headers: { 'content-type': 'application/json' } }))
    );

    await Promise.all([
      fetchAPI('/items', { method: 'POST', body: '{}' }),
      fetchAPI('/items', { method: 'POST', body: '{}' }),
      fetchAPI('/catalog', { timeout: 1000 }),
      fetchAPI('/catalog', { timeout: 1000 }),
    ]);

    expect(global.fetch).toHaveBeenCalledTimes(4);
  });
});
