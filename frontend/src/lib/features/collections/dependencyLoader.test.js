import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createDependencyLoader } from './dependencyLoader.svelte.js';

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe('createDependencyLoader', () => {
  let now;
  let fetchGroups;
  let loader;

  beforeEach(() => {
    now = 0;
    fetchGroups = vi.fn();
    loader = createDependencyLoader({
      chunkSize: 100,
      maxConcurrentChunks: 2,
      retryCooldownMs: 5000,
      now: () => now,
      fetchGroups,
    });
  });

  it('joins overlapping waves so a pending id is fetched once', async () => {
    const first = deferred();
    fetchGroups
      .mockImplementationOnce(() => first.promise)
      .mockImplementationOnce(() =>
        Promise.resolve({ 3: { outgoing: [{ id: 'c' }], incoming: [] } })
      );

    const a = loader.ensure([1, 2]);
    const b = loader.ensure([2, 3]);
    await Promise.resolve();
    await Promise.resolve();

    // The second wave owns the still-missing id 3 only; id 2 stays joined.
    expect(fetchGroups).toHaveBeenCalledTimes(2);
    expect(fetchGroups).toHaveBeenNthCalledWith(1, [1, 2]);
    expect(fetchGroups).toHaveBeenNthCalledWith(2, [3]);

    first.resolve({
      1: { outgoing: [{ id: 'a' }], incoming: [] },
      2: { outgoing: [], incoming: [{ id: 'b' }] },
    });
    await Promise.all([a, b]);

    expect(loader.get(1)).toMatchObject({ status: 'ready' });
    expect(loader.merged(2)).toEqual([{ id: 'b' }]);
  });

  it('commits successful chunks independently and marks failed ids retryable', async () => {
    loader = createDependencyLoader({
      chunkSize: 1,
      maxConcurrentChunks: 2,
      retryCooldownMs: 5000,
      now: () => now,
      fetchGroups,
    });

    fetchGroups.mockImplementation((ids) =>
      ids[0] === 2
        ? Promise.reject(new Error('boom'))
        : Promise.resolve({ [ids[0]]: { outgoing: [{ id: 'ok' }], incoming: [] } })
    );

    await loader.ensure([1, 2]);

    expect(loader.get(1)).toMatchObject({ status: 'ready' });
    expect(loader.get(2)).toMatchObject({ status: 'error' });
    expect(loader.merged(2)).toEqual([]);
  });

  it('treats an explicit empty group as ready but an omitted group as unknown', async () => {
    fetchGroups.mockResolvedValueOnce({ 1: { outgoing: [], incoming: [] } });

    await loader.ensure([1, 2]);

    expect(loader.get(1)).toMatchObject({ status: 'ready' });
    expect(loader.get(2)).toMatchObject({ status: 'error' });
  });

  it('retries failed ids only after the five-second cooldown', async () => {
    loader = createDependencyLoader({
      chunkSize: 1,
      retryCooldownMs: 5000,
      now: () => now,
      fetchGroups,
    });
    fetchGroups.mockRejectedValueOnce(new Error('boom'));

    await loader.ensure([1]);
    expect(loader.get(1).status).toBe('error');
    expect(fetchGroups).toHaveBeenCalledTimes(1);

    // Still inside the cooldown.
    now = 4000;
    await loader.ensure([1]);
    expect(fetchGroups).toHaveBeenCalledTimes(1);

    // Eligible again after the cooldown.
    now = 5001;
    fetchGroups.mockResolvedValueOnce({ 1: { outgoing: [], incoming: [] } });
    await loader.ensure([1]);
    expect(fetchGroups).toHaveBeenCalledTimes(2);
    expect(loader.get(1).status).toBe('ready');
  });

  it('drops in-flight writes after a scope reset', async () => {
    const pending = deferred();
    fetchGroups.mockReturnValueOnce(pending.promise);

    const request = loader.ensure([1]);
    loader.reset();
    pending.resolve({ 1: { outgoing: [{ id: 'stale' }], incoming: [] } });
    await request;

    expect(loader.get(1)).toBeNull();
  });

  it('marks a continuation-bearing group partial instead of exact', async () => {
    fetchGroups.mockResolvedValueOnce({
      1: { outgoing: [{ id: 'a' }], incoming: [], has_more_links: true, next_cursor: 'c1' },
    });

    await loader.ensure([1]);

    expect(loader.get(1)).toMatchObject({
      status: 'partial',
      hasMoreLinks: true,
      nextCursor: 'c1',
    });
  });

  it('retains prior successful data when a refresh chunk fails', async () => {
    loader = createDependencyLoader({
      chunkSize: 1,
      retryCooldownMs: 5000,
      now: () => now,
      fetchGroups,
    });
    fetchGroups.mockResolvedValueOnce({ 1: { outgoing: [{ id: 'keep' }], incoming: [] } });
    await loader.ensure([1]);

    loader.invalidate([1]);
    fetchGroups.mockRejectedValueOnce(new Error('boom'));
    await loader.ensure([1]);

    expect(loader.get(1)).toMatchObject({ status: 'stale' });
    expect(loader.merged(1)).toEqual([{ id: 'keep' }]);
  });
});
