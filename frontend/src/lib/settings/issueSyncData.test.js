import { describe, expect, it, vi } from 'vitest';
import { loadIssueSyncLinkedRepositories, loadIssueSyncPageData } from './issueSyncData.js';

describe('Issue Sync repository request graph', () => {
  it('loads repositories for every connection with one enriched request', async () => {
    const apiClient = {
      workspaceSCM: {
        getConnectionsOverview: vi.fn().mockResolvedValue([
          { id: 1, repositories: [{ id: 10 }] },
          { id: 2, repositories: [{ id: 20 }, { id: 21 }] },
        ]),
        getConnections: vi.fn(),
        getLinkedRepos: vi.fn(),
      },
    };

    const repositories = await loadIssueSyncLinkedRepositories(apiClient, 4);

    expect(apiClient.workspaceSCM.getConnectionsOverview).toHaveBeenCalledOnce();
    expect(apiClient.workspaceSCM.getConnectionsOverview).toHaveBeenCalledWith(4, {
      includeRepositories: true,
    });
    expect(apiClient.workspaceSCM.getConnections).not.toHaveBeenCalled();
    expect(apiClient.workspaceSCM.getLinkedRepos).not.toHaveBeenCalled();
    expect(repositories).toEqual([{ id: 10 }, { id: 20 }, { id: 21 }]);
  });

  it('loads setup concurrently from the enriched list and shared workspace snapshot', async () => {
    const apiClient = {
      issueSync: { getConfig: vi.fn().mockResolvedValue({ id: 8 }) },
      workspaceSCM: {
        getConnectionsOverview: vi.fn().mockResolvedValue([{ repositories: [{ id: 10 }] }]),
      },
      itemTypes: { getAll: vi.fn() },
      priorities: { getAll: vi.fn() },
      getAssignableUsers: vi.fn(),
      milestones: { getAll: vi.fn() },
    };
    const referenceStore = {
      itemTypes: [{ id: 1 }],
      priorities: [{ id: 2 }],
      users: [{ id: 3 }],
      milestones: [{ id: 4 }],
      initialize: vi.fn().mockResolvedValue(undefined),
    };

    const data = await loadIssueSyncPageData(apiClient, referenceStore, 4);

    expect(apiClient.issueSync.getConfig).toHaveBeenCalledWith(4);
    expect(apiClient.workspaceSCM.getConnectionsOverview).toHaveBeenCalledOnce();
    expect(referenceStore.initialize).toHaveBeenCalledWith(4);
    expect(apiClient.itemTypes.getAll).not.toHaveBeenCalled();
    expect(apiClient.priorities.getAll).not.toHaveBeenCalled();
    expect(apiClient.getAssignableUsers).not.toHaveBeenCalled();
    expect(apiClient.milestones.getAll).not.toHaveBeenCalled();
    expect(data).toEqual({
      config: { id: 8 },
      linkedRepositories: [{ id: 10 }],
      itemTypes: [{ id: 1 }],
      priorities: [{ id: 2 }],
      users: [{ id: 3 }],
      milestones: [{ id: 4 }],
    });
  });
});
