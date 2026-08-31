import { beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('../api.js', () => ({
  api: {
    getUsers: vi.fn(),
    assets: { getSummaries: vi.fn() },
  },
}));

import { api } from '../api.js';
import { referenceDisplayCache } from './referenceDisplayCache.svelte.js';

beforeEach(() => {
  referenceDisplayCache.reset();
  vi.clearAllMocks();
});

describe('reference display cache', () => {
  test('single-flights the full user list across renderers', async () => {
    api.getUsers.mockResolvedValue([{ id: 1, first_name: 'Ada' }]);

    await Promise.all([
      referenceDisplayCache.loadUsers(),
      referenceDisplayCache.loadUsers(),
      referenceDisplayCache.loadUsers(),
    ]);

    expect(api.getUsers).toHaveBeenCalledTimes(1);
    expect(referenceDisplayCache.users).toHaveLength(1);
  });

  test('coalesces cell lookups into one unique-ID batch and caches misses', async () => {
    api.assets.getSummaries.mockResolvedValue([
      { id: 1, set_id: 4, title: 'Laptop' },
      { id: 2, set_id: 4, title: 'Monitor' },
    ]);

    await Promise.all([
      referenceDisplayCache.loadAssets([1, 2]),
      referenceDisplayCache.loadAssets([2, 3]),
      referenceDisplayCache.loadAssets([1]),
    ]);

    expect(api.assets.getSummaries).toHaveBeenCalledTimes(1);
    expect(api.assets.getSummaries).toHaveBeenCalledWith([1, 2, 3], expect.anything());
    expect(referenceDisplayCache.getAsset(1)?.title).toBe('Laptop');
    expect(referenceDisplayCache.getAsset(3)).toBeNull();

    await referenceDisplayCache.loadAssets([1, 2, 3]);
    expect(api.assets.getSummaries).toHaveBeenCalledTimes(1);
  });

  test('drops work cancelled before the coalesced request starts', async () => {
    const controller = new AbortController();
    const load = referenceDisplayCache.loadAssets([9], { signal: controller.signal });
    controller.abort();
    await load;

    expect(api.assets.getSummaries).not.toHaveBeenCalled();
  });

  test('does not restore previous account references after reset', async () => {
    let resolveOldUsers;
    api.getUsers.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveOldUsers = resolve;
      })
    );

    const oldLoad = referenceDisplayCache.loadUsers();
    referenceDisplayCache.reset();
    resolveOldUsers([{ id: 4, first_name: 'Previous account' }]);
    await oldLoad;

    expect(referenceDisplayCache.users).toEqual([]);
  });
});
