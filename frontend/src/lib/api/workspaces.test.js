import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('./core.js', () => ({
  fetchAllV2Pages: vi.fn(),
  fetchAPI: vi.fn(),
  fetchV2Data: vi.fn(),
}));

vi.mock('./createCrudClient.js', () => ({
  createCrudClient: vi.fn(() => ({})),
}));

const { fetchAPI, fetchV2Data } = await import('./core.js');
const { workspaces } = await import('./workspaces.js');

describe('workspace API', () => {
  beforeEach(() => {
    fetchAPI.mockReset();
    fetchV2Data.mockReset();
  });

  it('loads the workspace reference graph through one bootstrap request', async () => {
    fetchAPI.mockResolvedValue({ workspace: { id: 42 } });

    await workspaces.getBootstrap(42);

    expect(fetchAPI).toHaveBeenCalledOnce();
    expect(fetchAPI).toHaveBeenCalledWith('/workspaces/42/bootstrap', expect.any(Object));
  });

  it('loads workspace-visible time projects from the canonical v2 owner path', async () => {
    fetchV2Data.mockResolvedValue([]);

    await workspaces.getProjects(42);

    expect(fetchV2Data).toHaveBeenCalledOnce();
    expect(fetchV2Data).toHaveBeenCalledWith('/workspaces/42/time-projects');
  });

  it('normalizes workspace-scoped v2 statuses', async () => {
    fetchV2Data.mockResolvedValue([
      {
        id: 3,
        category: {
          id: 30,
          name: 'Done',
          builtin_key: 'done',
          color: '#22c55e',
          is_completed: true,
        },
      },
    ]);

    await expect(workspaces.getStatuses(42)).resolves.toEqual([
      expect.objectContaining({
        category_id: 30,
        category_name: 'Done',
        category_builtin_key: 'done',
        category_color: '#22c55e',
        is_completed: true,
      }),
    ]);
  });
});
