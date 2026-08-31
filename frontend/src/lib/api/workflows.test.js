import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('./core.js', () => ({
  fetchAPI: vi.fn(),
}));

vi.mock('./createCrudClient.js', () => ({
  createCrudClient: vi.fn(() => ({})),
}));

const { fetchAPI } = await import('./core.js');
const { workflows } = await import('./workflows.js');

describe('workflow API', () => {
  beforeEach(() => fetchAPI.mockReset());

  it('requests all transitions through the enriched workflow list', async () => {
    fetchAPI.mockResolvedValue([]);

    await workflows.getAllWithTransitions();

    expect(fetchAPI).toHaveBeenCalledOnce();
    expect(fetchAPI).toHaveBeenCalledWith('/workflows?include_transitions=true');
  });
});
