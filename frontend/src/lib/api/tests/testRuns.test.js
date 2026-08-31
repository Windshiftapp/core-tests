import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../core.js', () => ({
  fetchAPI: vi.fn(),
}));

vi.mock('../createCrudClient.js', () => ({
  createCrudClient: vi.fn(() => ({})),
}));

const { fetchAPI } = await import('../core.js');
const { testRuns } = await import('./testRuns.js');

describe('test runs API', () => {
  beforeEach(() => fetchAPI.mockReset());

  it('loads the complete run graph through one detail request', async () => {
    fetchAPI.mockResolvedValue({ run: { id: 9 } });

    await testRuns.getDetail(3, 9);

    expect(fetchAPI).toHaveBeenCalledOnce();
    expect(fetchAPI).toHaveBeenCalledWith('/workspaces/3/test-runs/9/detail');
  });
});
