import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('./core.js', () => ({
  fetchAPI: vi.fn(),
}));

vi.mock('./createCrudClient.js', () => ({
  createCrudClient: vi.fn(() => ({})),
}));

const { fetchAPI } = await import('./core.js');
const { workspaceSCM } = await import('./scm.js');

describe('workspace SCM API', () => {
  beforeEach(() => fetchAPI.mockReset());

  it('requests accessible workspace connections in one call', async () => {
    fetchAPI.mockResolvedValue([]);

    await workspaceSCM.getAccessibleConnections();

    expect(fetchAPI).toHaveBeenCalledOnce();
    expect(fetchAPI).toHaveBeenCalledWith('/scm-connections');
  });

  it('requests optional repositories and auth summaries on the connection list', async () => {
    fetchAPI.mockResolvedValue([]);

    await workspaceSCM.getConnectionsOverview(7, {
      includeRepositories: true,
      includeAuthStatus: true,
    });

    expect(fetchAPI).toHaveBeenCalledWith(
      '/workspaces/7/scm-connections?include_repositories=true&include_auth_status=true'
    );
  });
});
