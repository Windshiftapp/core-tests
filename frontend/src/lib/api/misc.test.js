import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

describe('comments API reaction opt-in', () => {
  let fetchSpy;

  beforeEach(() => {
    vi.resetModules();
    fetchSpy = vi.fn().mockImplementation(() =>
      Promise.resolve(
        new Response(JSON.stringify({ data: { comments: [] } }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      )
    );
    vi.stubGlobal('fetch', fetchSpy);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('omits reactions from the comment feed unless the caller opts in', async () => {
    const { getComments } = await import('./misc.js');

    await getComments(42, { limit: 25 });
    expect(fetchSpy.mock.calls[0][0]).toBe('/api/v2/items/42/comments?page_size=25');

    await getComments(42, { limit: 25, cursor: 'abc', include_reactions: true });
    expect(fetchSpy.mock.calls[1][0]).toBe(
      '/api/v2/items/42/comments?page_size=25&cursor=abc&include_reactions=true'
    );
  });
});
