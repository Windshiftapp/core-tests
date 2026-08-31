import { describe, expect, it, vi } from 'vitest';
import { loadMilestoneReleaseConnections } from './milestoneReleaseData.js';

describe('milestone release connection request graph', () => {
  it('loads global connections with one accessible-workspaces request', async () => {
    const apiClient = {
      workspaceSCM: {
        getAccessibleConnections: vi.fn().mockResolvedValue([
          { id: 10, workspace_id: 1, workspace_name: 'One' },
          { id: 20, workspace_id: 2, workspace_name: 'Two' },
        ]),
        getConnections: vi.fn(),
      },
      workspaces: { getAll: vi.fn() },
    };

    const connections = await loadMilestoneReleaseConnections(apiClient, null);

    expect(apiClient.workspaceSCM.getAccessibleConnections).toHaveBeenCalledOnce();
    expect(apiClient.workspaceSCM.getConnections).not.toHaveBeenCalled();
    expect(apiClient.workspaces.getAll).not.toHaveBeenCalled();
    expect(connections).toEqual([
      { id: 10, workspace_id: 1, workspace_name: 'One', _workspaceId: 1, _workspaceName: 'One' },
      { id: 20, workspace_id: 2, workspace_name: 'Two', _workspaceId: 2, _workspaceName: 'Two' },
    ]);
  });

  it('keeps the existing single request for workspace milestones', async () => {
    const apiClient = {
      workspaceSCM: {
        getAccessibleConnections: vi.fn(),
        getConnections: vi.fn().mockResolvedValue([{ id: 30 }]),
      },
    };

    const connections = await loadMilestoneReleaseConnections(apiClient, 3);

    expect(apiClient.workspaceSCM.getConnections).toHaveBeenCalledWith(3);
    expect(apiClient.workspaceSCM.getAccessibleConnections).not.toHaveBeenCalled();
    expect(connections).toEqual([{ id: 30, _workspaceId: 3, _workspaceName: null }]);
  });
});
