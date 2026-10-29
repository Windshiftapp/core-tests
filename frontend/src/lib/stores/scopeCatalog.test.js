import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ getScopeCatalog: vi.fn() }));

vi.mock('../api/users.js', () => ({
  getScopeCatalog: mocks.getScopeCatalog,
}));

import { scopeCatalogStore } from './scopeCatalog.svelte.js';
import { getScopeCatalog } from '../api/users.js';

beforeEach(() => {
  vi.clearAllMocks();
  scopeCatalogStore.reset();
});

afterEach(() => vi.restoreAllMocks());

describe('scopeCatalogStore', () => {
  it('shares one in-flight request and caches the result', async () => {
    const catalog = [{ scope: 'items:read', description: 'Read items' }];
    mocks.getScopeCatalog.mockResolvedValue(catalog);

    const [first, second] = await Promise.all([scopeCatalogStore.load(), scopeCatalogStore.load()]);
    const cached = await scopeCatalogStore.load();

    expect(mocks.getScopeCatalog).toHaveBeenCalledOnce();
    expect(first).toEqual(catalog);
    expect(second).toEqual(catalog);
    expect(cached).toEqual(catalog);
  });

  it('exposes failure and allows a later retry', async () => {
    const failure = new Error('offline');
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    mocks.getScopeCatalog.mockRejectedValueOnce(failure).mockResolvedValueOnce([]);

    expect(await scopeCatalogStore.load()).toEqual([]);
    expect(scopeCatalogStore.error).toBe(failure);
    await scopeCatalogStore.load();

    expect(mocks.getScopeCatalog).toHaveBeenCalledTimes(2);
    expect(scopeCatalogStore.error).toBeNull();
  });
});
