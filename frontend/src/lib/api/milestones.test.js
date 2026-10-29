import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('./core.js', () => ({
  fetchAllV2Pages: vi.fn(),
  fetchV2Data: vi.fn(),
}));

vi.mock('./createCrudClient.js', () => ({
  createCrudClient: vi.fn(() => ({})),
}));

const { fetchAllV2Pages, fetchV2Data } = await import('./core.js');
const { iterations, milestones } = await import('./milestones.js');

describe('milestones API', () => {
  beforeEach(() => {
    fetchAllV2Pages.mockReset();
    fetchV2Data.mockReset();
  });

  it('loads workspace milestones with global rows in one scoped request', async () => {
    fetchAllV2Pages.mockResolvedValue([]);
    const controller = new AbortController();

    await milestones.getAll(
      { workspace_id: 23, include_global: true },
      { signal: controller.signal }
    );

    expect(fetchAllV2Pages).toHaveBeenCalledOnce();
    expect(fetchAllV2Pages).toHaveBeenCalledWith('/workspaces/23/milestones?include_global=true', {
      signal: controller.signal,
    });
  });

  it('defaults workspace-scoped iteration lists to include global rows', async () => {
    fetchAllV2Pages.mockResolvedValue([]);

    await iterations.getAll({ workspace_id: 5 });

    expect(fetchAllV2Pages).toHaveBeenCalledOnce();
    expect(fetchAllV2Pages).toHaveBeenCalledWith('/workspaces/5/iterations?include_global=true', {});
  });

  it('requests test statistics for unique milestone IDs in one call', async () => {
    fetchV2Data.mockResolvedValue([]);

    await milestones.getTestStatisticsMany([3, 4, 3]);

    expect(fetchV2Data).toHaveBeenCalledOnce();
    expect(fetchV2Data).toHaveBeenCalledWith('/milestones/test-statistics', {
      method: 'POST',
      body: JSON.stringify({ ids: [3, 4] }),
    });
  });

  it('sends the release idempotency key with the mutation', async () => {
    fetchV2Data.mockResolvedValue({});

    await milestones.release(9, { tag_name: 'v0.8.4' }, 'release-request-1');

    expect(fetchV2Data).toHaveBeenCalledWith('/milestones/9/release', {
      method: 'POST',
      headers: { 'Idempotency-Key': 'release-request-1' },
      body: JSON.stringify({ tag_name: 'v0.8.4' }),
    });
  });

  it('creates a workspace-scoped milestone on the workspace path', async () => {
    fetchV2Data.mockResolvedValue({});

    await milestones.create({ name: 'Release', is_global: false, workspace_id: 23 });

    expect(fetchV2Data).toHaveBeenCalledWith('/workspaces/23/milestones', {
      method: 'POST',
      body: JSON.stringify({ name: 'Release' }),
    });
  });

  it('creates a global milestone on the global path', async () => {
    fetchV2Data.mockResolvedValue({});

    await milestones.create({ name: 'Release', is_global: true, workspace_id: null });

    expect(fetchV2Data).toHaveBeenCalledWith('/milestones', {
      method: 'POST',
      body: JSON.stringify({ name: 'Release' }),
    });
  });
});
