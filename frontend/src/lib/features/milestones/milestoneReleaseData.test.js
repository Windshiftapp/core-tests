import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api/scm.js', () => ({
  workspaceSCM: { getAccessibleConnections: vi.fn(), getConnections: vi.fn() },
}));

import { workspaceSCM } from '../../api/scm.js';
import { loadMilestoneReleaseConnections } from './milestoneReleaseData.js';

describe('milestone release connection request graph', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('loads global connections with one accessible-workspaces request', async () => {
    workspaceSCM.getAccessibleConnections.mockResolvedValue([
      { id: 10, workspace_id: 1, workspace_name: 'One' },
      { id: 20, workspace_id: 2, workspace_name: 'Two' },
    ]);

    const connections = await loadMilestoneReleaseConnections(null);

    expect(workspaceSCM.getAccessibleConnections).toHaveBeenCalledOnce();
    expect(workspaceSCM.getConnections).not.toHaveBeenCalled();
    expect(connections).toEqual([
      { id: 10, workspace_id: 1, workspace_name: 'One', _workspaceId: 1, _workspaceName: 'One' },
      { id: 20, workspace_id: 2, workspace_name: 'Two', _workspaceId: 2, _workspaceName: 'Two' },
    ]);
  });

  it('keeps the existing single request for workspace milestones', async () => {
    workspaceSCM.getConnections.mockResolvedValue([{ id: 30 }]);

    const connections = await loadMilestoneReleaseConnections(3);

    expect(workspaceSCM.getConnections).toHaveBeenCalledWith(3);
    expect(workspaceSCM.getAccessibleConnections).not.toHaveBeenCalled();
    expect(connections).toEqual([{ id: 30, _workspaceId: 3, _workspaceName: null }]);
  });
});
