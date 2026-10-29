import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api/sla.js', () => ({
  sla: {},
}));

import { sla } from '../../api/sla.js';
import { getItemSLA, getSLAThresholds, invalidateSLAState, invalidateSLAThresholds, resetSLAState } from './slaState.js';

function flushMicrotasks() {
  return new Promise((resolve) => queueMicrotask(resolve));
}

describe('slaState batch coalescing', () => {
  beforeEach(() => {
    resetSLAState();
    vi.clearAllMocks();
    // Assignments in earlier tests persist on the shared mock object.
    delete sla.getItemSLABatch;
    delete sla.getItemSLA;
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('coalesces same-tick reads for one workspace into a single batch request', async () => {
    sla.getItemSLABatch = vi.fn().mockResolvedValue({
      11: [{ metric_id: 1, metric_name: 'First response' }],
      12: [{ metric_id: 1, metric_name: 'First response' }],
    });

    const first = getItemSLA(11, 5);
    const second = getItemSLA(12, 5);
    await Promise.all([first, second]);
    await flushMicrotasks();

    expect(sla.getItemSLABatch).toHaveBeenCalledTimes(1);
    expect(sla.getItemSLABatch).toHaveBeenCalledWith(
      5,
      [11, 12],
      expect.objectContaining({ readPolicy: { priority: 'background' } })
    );
    expect(await first).toEqual([{ metric_id: 1, metric_name: 'First response' }]);
    expect(await second).toEqual([{ metric_id: 1, metric_name: 'First response' }]);
    // The per-item fallback must stay unused when the batch succeeds.
    expect(sla.getItemSLA?.mock?.calls?.length ?? 0).toBe(0);
  });

  it('does not fan out per-item reads when the batch request fails', async () => {
    sla.getItemSLABatch = vi.fn().mockRejectedValue(new Error('boom'));
    sla.getItemSLA = vi.fn().mockResolvedValue([{ metric_id: 2, metric_name: 'Fallback' }]);

    const first = getItemSLA(13, 5);
    const second = getItemSLA(14, 5);
    const [firstValue, secondValue] = await Promise.all([first, second]);
    await flushMicrotasks();

    expect(sla.getItemSLABatch).toHaveBeenCalledTimes(1);
    expect(sla.getItemSLA).not.toHaveBeenCalled();
    expect(firstValue).toEqual([]);
    expect(secondValue).toEqual([]);
  });

  it('retries the batch on the next read after a failed batch instead of caching the failure', async () => {
    sla.getItemSLABatch = vi
      .fn()
      .mockRejectedValueOnce(new Error('boom'))
      .mockResolvedValueOnce({ 17: [{ metric_id: 4, metric_name: 'Recovered' }] });

    expect(await getItemSLA(17, 5)).toEqual([]);
    await flushMicrotasks();

    expect(await getItemSLA(17, 5)).toEqual([{ metric_id: 4, metric_name: 'Recovered' }]);
    expect(sla.getItemSLABatch).toHaveBeenCalledTimes(2);
  });

  it('chunks over-limit id sets and merges every chunk result', async () => {
    const ids = Array.from({ length: 250 }, (_, index) => index + 1);
    sla.getItemSLABatch = vi
      .fn()
      .mockImplementation((_workspaceId, chunkIds) =>
        Promise.resolve(
          Object.fromEntries(
            chunkIds.map((id) => [id, [{ metric_id: 7, metric_name: `Metric ${id}` }]])
          )
        )
      );

    const values = await Promise.all(ids.map((id) => getItemSLA(id, 5)));
    await flushMicrotasks();

    expect(sla.getItemSLABatch).toHaveBeenCalledTimes(2);
    expect(sla.getItemSLABatch.mock.calls[0][1]).toHaveLength(200);
    expect(sla.getItemSLABatch.mock.calls[1][1]).toHaveLength(50);
    expect(values).toHaveLength(250);
    expect(values[0]).toEqual([{ metric_id: 7, metric_name: 'Metric 1' }]);
    expect(values[249]).toEqual([{ metric_id: 7, metric_name: 'Metric 250' }]);
  });

  it('resolves unset ids from one failed chunk while keeping a successful chunk', async () => {
    const ids = Array.from({ length: 250 }, (_, index) => index + 1);
    sla.getItemSLABatch = vi.fn().mockImplementation((_workspaceId, chunkIds) => {
      if (chunkIds.length === 200) return Promise.reject(new Error('boom'));
      return Promise.resolve(
        Object.fromEntries(
          chunkIds.map((id) => [id, [{ metric_id: 7, metric_name: `Metric ${id}` }]])
        )
      );
    });

    const values = await Promise.all(ids.map((id) => getItemSLA(id, 5)));
    await flushMicrotasks();

    expect(values[0]).toEqual([]);
    expect(values[199]).toEqual([]);
    expect(values[200]).toEqual([{ metric_id: 7, metric_name: 'Metric 201' }]);
    expect(values[249]).toEqual([{ metric_id: 7, metric_name: 'Metric 250' }]);
  });

  it('issues separate batch requests per workspace', async () => {
    sla.getItemSLABatch = vi.fn().mockResolvedValue({});

    await Promise.all([getItemSLA(14, 5), getItemSLA(15, 6)]);
    await flushMicrotasks();

    const workspaces = sla.getItemSLABatch.mock.calls.map((call) => call[0]);
    expect(workspaces.sort()).toEqual([5, 6]);
  });

  it('uses the single-item read when batching is unavailable', async () => {
    sla.getItemSLA = vi.fn().mockResolvedValue([{ metric_id: 3 }]);

    const value = await getItemSLA(16, 5);

    expect(value).toEqual([{ metric_id: 3 }]);
    expect(sla.getItemSLA).toHaveBeenCalledWith(
      16,
      expect.objectContaining({ readPolicy: { priority: 'background' } })
    );
  });

  it('resolves two same-tick reads for one item from the same batch instead of returning a resolver', async () => {
    sla.getItemSLABatch = vi.fn().mockResolvedValue({
      21: [{ metric_id: 9, metric_name: 'Dedup' }],
    });

    const first = getItemSLA(21, 5);
    const second = getItemSLA(21, 5);
    const [firstValue, secondValue] = await Promise.all([first, second]);
    await flushMicrotasks();

    expect(sla.getItemSLABatch).toHaveBeenCalledTimes(1);
    expect(sla.getItemSLABatch).toHaveBeenCalledWith(
      5,
      [21],
      expect.objectContaining({ readPolicy: { priority: 'background' } })
    );
    expect(firstValue).toEqual([{ metric_id: 9, metric_name: 'Dedup' }]);
    expect(secondValue).toEqual([{ metric_id: 9, metric_name: 'Dedup' }]);
    expect(typeof secondValue).not.toBe('function');
  });

  it('reuses the in-flight batch when the same item is requested again during the flush', async () => {
    let resolveBatch;
    sla.getItemSLABatch = vi.fn().mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveBatch = resolve;
        })
    );

    const first = getItemSLA(22, 5);
    await flushMicrotasks(); // flush started; request still pending
    const second = getItemSLA(22, 5);
    resolveBatch({ 22: [{ metric_id: 5, metric_name: 'In flight' }] });
    const [firstValue, secondValue] = await Promise.all([first, second]);

    expect(sla.getItemSLABatch).toHaveBeenCalledTimes(1);
    expect(firstValue).toEqual([{ metric_id: 5, metric_name: 'In flight' }]);
    expect(secondValue).toEqual([{ metric_id: 5, metric_name: 'In flight' }]);
  });
});

describe('slaState threshold freshness', () => {
  beforeEach(() => {
    resetSLAState();
    vi.clearAllMocks();
    sla.getWarningThresholds = vi.fn();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('caches thresholds for five minutes while item states keep the 30s cadence', async () => {
    vi.useFakeTimers();
    sla.getWarningThresholds.mockResolvedValue([{ id: 1, percent: 75 }]);

    await getSLAThresholds(5);
    await getSLAThresholds(5);
    expect(sla.getWarningThresholds).toHaveBeenCalledTimes(1);

    // A board tick only invalidates item states, so thresholds stay cached.
    invalidateSLAState();
    await vi.advanceTimersByTimeAsync(30_000);
    await getSLAThresholds(5);
    expect(sla.getWarningThresholds).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(5 * 60 * 1000);
    await getSLAThresholds(5);
    expect(sla.getWarningThresholds).toHaveBeenCalledTimes(2);
  });

  it('shares one threshold request across overlapping callers', async () => {
    let resolve;
    sla.getWarningThresholds.mockReturnValue(
      new Promise((r) => {
        resolve = r;
      })
    );

    const first = getSLAThresholds(5);
    const second = getSLAThresholds(5);
    expect(sla.getWarningThresholds).toHaveBeenCalledTimes(1);

    resolve([{ id: 1 }]);
    await expect(Promise.all([first, second])).resolves.toEqual([[{ id: 1 }], [{ id: 1 }]]);
  });

  it('retains stale thresholds on transient failure instead of caching an empty list', async () => {
    vi.useFakeTimers();
    sla.getWarningThresholds.mockResolvedValueOnce([{ id: 1 }]);
    await getSLAThresholds(5);

    await vi.advanceTimersByTimeAsync(5 * 60 * 1000 + 1);
    sla.getWarningThresholds.mockRejectedValueOnce(new Error('offline'));
    await expect(getSLAThresholds(5)).resolves.toEqual([{ id: 1 }]);

    // Five-second cooldown suppresses an immediate retry.
    await expect(getSLAThresholds(5)).resolves.toEqual([{ id: 1 }]);
    expect(sla.getWarningThresholds).toHaveBeenCalledTimes(2);
  });

  it('reports threshold-unavailable (null) when no prior value exists', async () => {
    sla.getWarningThresholds.mockRejectedValueOnce(new Error('offline'));
    await expect(getSLAThresholds(6)).resolves.toBeNull();
  });

  it('clears protected thresholds on an authorization denial', async () => {
    sla.getWarningThresholds.mockResolvedValueOnce([{ id: 1 }]);
    await getSLAThresholds(7);

    invalidateSLAThresholds(7);
    sla.getWarningThresholds.mockRejectedValueOnce({ status: 403 });
    await expect(getSLAThresholds(7)).resolves.toBeNull();
  });

  it('does not let an invalidated pending request repopulate the cache', async () => {
    let resolve;
    sla.getWarningThresholds.mockReturnValueOnce(
      new Promise((r) => {
        resolve = r;
      })
    );
    const pending = getSLAThresholds(8);
    invalidateSLAThresholds(8);
    resolve([{ id: 1 }]);
    await pending;

    sla.getWarningThresholds.mockResolvedValueOnce([{ id: 2 }]);
    await expect(getSLAThresholds(8)).resolves.toEqual([{ id: 2 }]);
    expect(sla.getWarningThresholds).toHaveBeenCalledTimes(2);
  });

  it('does not discard another workspace\'s in-flight fetch when one is invalidated', async () => {
    let resolveFive;
    sla.getWarningThresholds.mockImplementation((workspaceId) => {
      if (Number(workspaceId) === 5) {
        return new Promise((r) => {
          resolveFive = r;
        });
      }
      return Promise.resolve([{ id: 9 }]);
    });

    const pendingFive = getSLAThresholds(5);
    const pendingSix = getSLAThresholds(6);
    // Invalidating workspace 6 must not prevent workspace 5 from caching.
    invalidateSLAThresholds(6);

    resolveFive([{ id: 1 }]);
    await expect(pendingFive).resolves.toEqual([{ id: 1 }]);
    await expect(pendingSix).resolves.toEqual([{ id: 9 }]);

    // The successful workspace 5 value was cached and is reused.
    await getSLAThresholds(5);
    expect(sla.getWarningThresholds).toHaveBeenCalledTimes(2);
  });

  it('reports unknown (null) for a missing workspace instead of configured-empty', async () => {
    await expect(getSLAThresholds(null)).resolves.toBeNull();
    expect(sla.getWarningThresholds).not.toHaveBeenCalled();
  });

  it('clears thresholds on session reset', async () => {
    sla.getWarningThresholds.mockResolvedValue([{ id: 1 }]);
    await getSLAThresholds(5);

    resetSLAState();
    await getSLAThresholds(5);

    expect(sla.getWarningThresholds).toHaveBeenCalledTimes(2);
  });
});
