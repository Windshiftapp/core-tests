import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api/scm.js', () => ({
  workspaceSCM: {
    getAvailableProviders: vi.fn(),
    getConnectionsOverview: vi.fn(),
    getConnections: vi.fn(),
    getAuthStatus: vi.fn(),
  },
}));

import { workspaceSCM } from '../api/scm.js';
import { loadWorkspaceSCMOverview } from './workspaceSCMData.js';

describe('Workspace SCM auth request graph', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('loads every connection auth summary in the enriched list', async () => {
    workspaceSCM.getAvailableProviders.mockResolvedValue([{ id: 3 }]);
    workspaceSCM.getConnectionsOverview.mockResolvedValue([
      { id: 10, auth_status: { auth_method: 'oauth', is_authenticated: true } },
      { id: 20, auth_status: { auth_method: 'pat', is_authenticated: false } },
    ]);

    const overview = await loadWorkspaceSCMOverview(4);

    expect(workspaceSCM.getAvailableProviders).toHaveBeenCalledWith(4);
    expect(workspaceSCM.getConnectionsOverview).toHaveBeenCalledWith(4, {
      includeAuthStatus: true,
    });
    expect(workspaceSCM.getConnections).not.toHaveBeenCalled();
    expect(workspaceSCM.getAuthStatus).not.toHaveBeenCalled();
    expect(overview.authStatuses).toEqual({
      10: { auth_method: 'oauth', is_authenticated: true },
      20: { auth_method: 'pat', is_authenticated: false },
    });
  });
});
