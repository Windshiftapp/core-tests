import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

describe('portal bootstrap API', () => {
  let fetchSpy;

  beforeEach(() => {
    fetchSpy = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({}), {
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

  it('loads the public portal shell with one aggregate request', async () => {
    const { portal } = await import('./portal.js');

    await portal.getBootstrap('support');

    expect(fetchSpy).toHaveBeenCalledOnce();
    expect(fetchSpy.mock.calls[0][0]).toBe('/api/portal/support/bootstrap');
  });

  it('loads authenticated portal-user state with one aggregate request', async () => {
    const { portal } = await import('./portal.js');

    await portal.getUserBootstrap('support');

    expect(fetchSpy).toHaveBeenCalledOnce();
    expect(fetchSpy.mock.calls[0][0]).toBe('/api/portal/support/user-bootstrap');
  });
});
