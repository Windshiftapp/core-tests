import { describe, expect, it, vi } from 'vitest';
import { loadWorkspaceSCMOverview } from './workspaceSCMData.js';

describe('Workspace SCM auth request graph', () => {
  it('loads every connection auth summary in the enriched list', async () => {
    const apiClient = {
      workspaceSCM: {
        getAvailableProviders: vi.fn().mockResolvedValue([{ id: 3 }]),
        getConnectionsOverview: vi.fn().mockResolvedValue([
          { id: 10, auth_status: { auth_method: 'oauth', is_authenticated: true } },
          { id: 20, auth_status: { auth_method: 'pat', is_authenticated: false } },
        ]),
        getConnections: vi.fn(),
        getAuthStatus: vi.fn(),
      },
    };

    const overview = await loadWorkspaceSCMOverview(apiClient, 4);

    expect(apiClient.workspaceSCM.getAvailableProviders).toHaveBeenCalledWith(4);
    expect(apiClient.workspaceSCM.getConnectionsOverview).toHaveBeenCalledWith(4, {
      includeAuthStatus: true,
    });
    expect(apiClient.workspaceSCM.getConnections).not.toHaveBeenCalled();
    expect(apiClient.workspaceSCM.getAuthStatus).not.toHaveBeenCalled();
    expect(overview.authStatuses).toEqual({
      10: { auth_method: 'oauth', is_authenticated: true },
      20: { auth_method: 'pat', is_authenticated: false },
    });
  });
});
