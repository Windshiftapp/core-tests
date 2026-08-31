import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('./core.js', () => ({
  API_BASE: '/api',
  fetchAPI: vi.fn(),
}));

vi.mock('./createCrudClient.js', () => ({
  createCrudClient: vi.fn(() => ({})),
}));

const { fetchAPI } = await import('./core.js');
const { screens } = await import('./configuration.js');

describe('screen API', () => {
  beforeEach(() => fetchAPI.mockReset());

  it('requests all screen fields through the enriched list', async () => {
    fetchAPI.mockResolvedValue([]);

    await screens.getAllWithFields();

    expect(fetchAPI).toHaveBeenCalledOnce();
    expect(fetchAPI).toHaveBeenCalledWith('/screens?include_fields=true');
  });
});
