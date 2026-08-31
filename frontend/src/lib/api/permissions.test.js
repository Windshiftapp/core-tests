import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

describe('permissions API bulk reads', () => {
  let fetchSpy;

  beforeEach(() => {
    fetchSpy = vi.fn().mockResolvedValue(
      new Response(JSON.stringify([]), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    );
    vi.stubGlobal('fetch', fetchSpy);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('loads all effective user global permissions with one request', async () => {
    const { permissions } = await import('./permissions.js');

    await permissions.getAllUserGlobalPermissions();

    expect(fetchSpy).toHaveBeenCalledOnce();
    expect(fetchSpy.mock.calls[0][0]).toBe('/api/users/permissions/global');
  });
});
