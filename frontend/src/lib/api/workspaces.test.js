import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('./core.js', () => ({
  fetchAPI: vi.fn(),
}));

vi.mock('./createCrudClient.js', () => ({
  createCrudClient: vi.fn(() => ({})),
}));

const { fetchAPI } = await import('./core.js');
const { workspaces } = await import('./workspaces.js');

describe('workspace API', () => {
  beforeEach(() => fetchAPI.mockReset());

  it('loads the workspace reference graph through one bootstrap request', async () => {
    fetchAPI.mockResolvedValue({ workspace: { id: 42 } });

    await workspaces.getBootstrap(42);

    expect(fetchAPI).toHaveBeenCalledOnce();
    expect(fetchAPI).toHaveBeenCalledWith('/workspaces/42/bootstrap');
  });
});
