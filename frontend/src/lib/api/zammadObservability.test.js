import { afterEach, describe, expect, it, vi } from 'vitest';
import { zammadTickets } from './integrations.js';

afterEach(() => vi.unstubAllGlobals());

describe('Zammad observation API', () => {
  it.each([
    ['item timeline', () => zammadTickets.history(99, { limit: 6 }), '/api/items/99/zammad-history?limit=6'],
    ['workspace overview', () => zammadTickets.workspaceOverview(23, { limit: 5 }), '/api/workspaces/23/zammad-overview?limit=5'],
  ])('loads the %s endpoint', async (_label, request, path) => {
    const response = { recent_changes: [] };
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(response), {
      status: 200, headers: { 'content-type': 'application/json' },
    }));
    vi.stubGlobal('fetch', fetchMock);

    await expect(request()).resolves.toEqual(response);
    expect(fetchMock).toHaveBeenCalledOnce();
    expect(fetchMock.mock.calls[0][0]).toBe(path);
  });
});
