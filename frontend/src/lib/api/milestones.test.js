import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('./core.js', () => ({
  fetchAPI: vi.fn(),
}));

vi.mock('./createCrudClient.js', () => ({
  createCrudClient: vi.fn(() => ({})),
}));

const { fetchAPI } = await import('./core.js');
const { milestones } = await import('./milestones.js');

describe('milestones API', () => {
  beforeEach(() => fetchAPI.mockReset());

  it('requests test statistics for unique milestone IDs in one call', async () => {
    fetchAPI.mockResolvedValue({});

    await milestones.getTestStatisticsMany([3, 4, 3]);

    expect(fetchAPI).toHaveBeenCalledOnce();
    expect(fetchAPI).toHaveBeenCalledWith('/milestones/test-statistics?ids=3,4');
  });

  it('sends the release idempotency key with the mutation', async () => {
    fetchAPI.mockResolvedValue({});

    await milestones.release(9, { tag_name: 'v0.8.4' }, 'release-request-1');

    expect(fetchAPI).toHaveBeenCalledWith('/milestones/9/release', {
      method: 'POST',
      headers: { 'Idempotency-Key': 'release-request-1' },
      body: JSON.stringify({ tag_name: 'v0.8.4' }),
    });
  });
});
