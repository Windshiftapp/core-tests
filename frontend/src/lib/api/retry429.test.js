import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('../utils/serverClock.js', () => ({
  updateOffset: vi.fn(),
  getClockOffset: vi.fn(() => 0),
  getSampleCount: vi.fn(() => 0),
  isClockDriftSignificant: vi.fn(() => false),
}));

import { getReadSchedulerState, resetReadScheduler } from './readScheduler.js';
import { clearAPIRequestSessionKey, concurrencyRetryDelayMs, fetchAPI, setAPIRequestSessionKey } from './core.js';

function makeResponse({ status = 200, statusText = 'OK', body = '', headers = {} } = {}) {
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

function tooManyRequests(extraHeaders = {}, body = '{"error":"too many requests","code":"RATE_LIMITED"}') {
  return makeResponse({ status: 429, statusText: 'Too Many Requests', body, headers: extraHeaders });
}

function ok(body = '{"ok":true}') {
  return makeResponse({ body, headers: { 'content-type': 'application/json' } });
}

beforeEach(() => {
  clearAPIRequestSessionKey();
  resetReadScheduler();
  vi.clearAllMocks();
  vi.spyOn(Math, 'random').mockReturnValue(0);
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe('concurrencyRetryDelayMs', () => {
  test('derives the delay from Retry-After, HTTP-date, or the 1s/2s fallback', () => {
    const noJitter = () => 0;
    expect(concurrencyRetryDelayMs({ retryAfter: '1' }, 0, noJitter)).toBe(1000);
    expect(concurrencyRetryDelayMs({ retryAfter: null }, 0, noJitter)).toBe(1000);
    expect(concurrencyRetryDelayMs({}, 1, noJitter)).toBe(2000);
    expect(concurrencyRetryDelayMs({}, 5, noJitter)).toBe(2000);
    expect(concurrencyRetryDelayMs({ retryAfter: 'not-a-date' }, 0, noJitter)).toBe(1000);

    const serverDate = new Date('2026-10-14T10:00:00Z');
    const retryAt = new Date(serverDate.getTime() + 3000).toUTCString();
    expect(
      concurrencyRetryDelayMs(
        { retryAfter: retryAt, serverDate: serverDate.toUTCString() },
        0,
        noJitter
      )
    ).toBe(3000);

    // Jitter only ever adds to the requested minimum delay.
    expect(concurrencyRetryDelayMs({ retryAfter: '1' }, 0, () => 1)).toBe(1250);
  });
});

describe('fetchAPI — 429 Retry-After recovery', () => {
  test('resolves once when a 429 is followed by a 200', async () => {
    vi.useFakeTimers();
    global.fetch = vi.fn().mockResolvedValueOnce(tooManyRequests({ 'Retry-After': '2' })).mockResolvedValueOnce(ok());

    const promise = fetchAPI('/items');
    await vi.advanceTimersByTimeAsync(0);
    expect(global.fetch).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(2000);
    await expect(promise).resolves.toEqual({ ok: true });
    expect(global.fetch).toHaveBeenCalledTimes(2);
  });

  test('rejects with the final original 429 error after three attempts', async () => {
    vi.useFakeTimers();
    global.fetch = vi.fn(() => Promise.resolve(tooManyRequests({ 'Retry-After': '1' })));

    const promise = fetchAPI('/items');
    const assertion = expect(promise).rejects.toMatchObject({
      status: 429,
      code: 'RATE_LIMITED',
      message: 'too many requests',
    });
    await vi.advanceTimersByTimeAsync(1000);
    await vi.advanceTimersByTimeAsync(2000);

    await assertion;
    expect(global.fetch).toHaveBeenCalledTimes(3);
  });

  test('falls back to 1s then 2s when Retry-After is malformed', async () => {
    vi.useFakeTimers();
    global.fetch = vi.fn(() => Promise.resolve(tooManyRequests({ 'Retry-After': 'not-a-date' })));

    const promise = fetchAPI('/items');
    const assertion = expect(promise).rejects.toMatchObject({ status: 429 });
    await vi.advanceTimersByTimeAsync(0);
    expect(global.fetch).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(1000);
    expect(global.fetch).toHaveBeenCalledTimes(2);

    await vi.advanceTimersByTimeAsync(2000);
    await assertion;
    expect(global.fetch).toHaveBeenCalledTimes(3);
  });

  test('resolves an HTTP-date Retry-After against the server Date header', async () => {
    vi.useFakeTimers();
    const serverNow = Date.parse('2026-10-14T10:00:00Z');
    const retryAt = new Date(serverNow + 5000).toUTCString();
    global.fetch = vi
      .fn()
      .mockResolvedValueOnce(
        tooManyRequests({ 'Retry-After': retryAt, Date: new Date(serverNow).toUTCString() })
      )
      .mockResolvedValueOnce(ok());

    const promise = fetchAPI('/items');
    await vi.advanceTimersByTimeAsync(0);
    // Less than the requested delay must not retry.
    await vi.advanceTimersByTimeAsync(1000);
    expect(global.fetch).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(4000);
    await expect(promise).resolves.toEqual({ ok: true });
    expect(global.fetch).toHaveBeenCalledTimes(2);
  });

  test('returns the last 429 immediately when the requested delay exceeds the deadline', async () => {
    vi.useFakeTimers();
    global.fetch = vi.fn(() => Promise.resolve(tooManyRequests({ 'Retry-After': '120' })));

    await expect(fetchAPI('/items')).rejects.toMatchObject({ status: 429 });
    expect(global.fetch).toHaveBeenCalledTimes(1);
    // No backoff timer was scheduled, so time does not change the outcome.
    await vi.advanceTimersByTimeAsync(60000);
    expect(global.fetch).toHaveBeenCalledTimes(1);
  });

  test('caller abort during backoff rejects and stops retrying', async () => {
    vi.useFakeTimers();
    const controller = new AbortController();
    global.fetch = vi.fn().mockResolvedValueOnce(tooManyRequests({ 'Retry-After': '5' })).mockResolvedValueOnce(ok());

    const promise = fetchAPI('/items', { signal: controller.signal });
    const assertion = expect(promise).rejects.toMatchObject({ name: 'AbortError' });
    await vi.advanceTimersByTimeAsync(0);
    expect(global.fetch).toHaveBeenCalledTimes(1);

    controller.abort();
    await assertion;
    await vi.advanceTimersByTimeAsync(10000);
    expect(global.fetch).toHaveBeenCalledTimes(1);
  });

  test('duplicate callers share one retry sequence', async () => {
    vi.useFakeTimers();
    setAPIRequestSessionKey('session-a');
    global.fetch = vi.fn().mockResolvedValueOnce(tooManyRequests({ 'Retry-After': '1' })).mockResolvedValueOnce(ok());

    const first = fetchAPI('/items');
    const second = fetchAPI('/items');
    expect(first).toBe(second);

    const assertion = Promise.all([first, second]);
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(1000);

    await expect(assertion).resolves.toEqual([{ ok: true }, { ok: true }]);
    expect(global.fetch).toHaveBeenCalledTimes(2);
  });

  test('a session change stops the retry sequence before another attempt', async () => {
    vi.useFakeTimers();
    setAPIRequestSessionKey('session-a');
    global.fetch = vi.fn().mockResolvedValueOnce(tooManyRequests({ 'Retry-After': '1' })).mockResolvedValueOnce(ok());

    const promise = fetchAPI('/items');
    const assertion = expect(promise).rejects.toMatchObject({ status: 429 });
    await vi.advanceTimersByTimeAsync(0);

    setAPIRequestSessionKey('session-b');
    // Finish the in-progress backoff; the loop must then stop without a retry.
    await vi.advanceTimersByTimeAsync(2000);
    await assertion;
    await vi.advanceTimersByTimeAsync(5000);
    expect(global.fetch).toHaveBeenCalledTimes(1);
  });

  test('never retries mutations', async () => {
    vi.useFakeTimers();
    global.fetch = vi.fn(() => Promise.resolve(tooManyRequests({ 'Retry-After': '1' })));

    await expect(
      fetchAPI('/items', { method: 'POST', body: '{}' })
    ).rejects.toMatchObject({ status: 429 });
    expect(global.fetch).toHaveBeenCalledTimes(1);

    await expect(
      fetchAPI('/items/1', { method: 'PATCH', body: '{}' })
    ).rejects.toMatchObject({ status: 429 });
    expect(global.fetch).toHaveBeenCalledTimes(2);
  });

  test('does not retry 5xx or network errors', async () => {
    vi.useFakeTimers();
    global.fetch = vi
      .fn()
      .mockResolvedValueOnce(makeResponse({ status: 503, statusText: 'Unavailable', body: '{}' }))
      .mockRejectedValueOnce(new TypeError('Failed to fetch'));

    await expect(fetchAPI('/items')).rejects.toMatchObject({ status: 503 });
    await expect(fetchAPI('/items')).rejects.toMatchObject({ code: 'NETWORK_ERROR' });
    expect(global.fetch).toHaveBeenCalledTimes(2);
  });

  test('releases the scheduler slot during backoff and reacquires per attempt', async () => {
    vi.useFakeTimers();
    global.fetch = vi.fn().mockResolvedValueOnce(tooManyRequests({ 'Retry-After': '1' })).mockResolvedValueOnce(ok());

    const promise = fetchAPI('/items', { readPolicy: { priority: 'foreground' } });
    await vi.advanceTimersByTimeAsync(0);

    // The failed physical attempt released its slot before the backoff sleep.
    expect(getReadSchedulerState().active).toBe(0);

    await vi.advanceTimersByTimeAsync(1000);
    await expect(promise).resolves.toEqual({ ok: true });
    expect(getReadSchedulerState().active).toBe(0);
  });
});
