import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('./core.js', () => ({
  fetchAPI: vi.fn(),
}));

vi.mock('./createCrudClient.js', () => ({
  createCrudClient: vi.fn(() => ({})),
}));

const { fetchAPI } = await import('./core.js');
const { collections } = await import('./collections.js');

describe('collections API', () => {
  beforeEach(() => fetchAPI.mockReset());

  it('requests the Board Configuration bootstrap with workspace fallback context', async () => {
    fetchAPI.mockResolvedValue({});

    await collections.getBoardConfigurationBootstrap(9, 4);

    expect(fetchAPI).toHaveBeenCalledOnce();
    expect(fetchAPI).toHaveBeenCalledWith(
      '/collections/9/board-configuration/bootstrap?workspace_id=4'
    );
  });

  it('requests a default workspace Board Configuration bootstrap', async () => {
    fetchAPI.mockResolvedValue({});

    await collections.getBoardConfigurationBootstrap(null, 4);

    expect(fetchAPI).toHaveBeenCalledWith(
      '/collections/default/board-configuration/bootstrap?workspace_id=4'
    );
  });
});
