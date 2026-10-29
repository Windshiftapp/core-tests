import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api/scm.js', () => ({
  issueSync: { getConfig: vi.fn() },
  workspaceSCM: {
    getConnectionsOverview: vi.fn(),
    getConnections: vi.fn(),
    getLinkedRepos: vi.fn(),
  },
}));

import { issueSync, workspaceSCM } from '../api/scm.js';
import { loadIssueSyncLinkedRepositories, loadIssueSyncPageData } from './issueSyncData.js';

describe('Issue Sync repository request graph', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('loads repositories for every connection with one enriched request', async () => {
    workspaceSCM.getConnectionsOverview.mockResolvedValue([
      { id: 1, repositories: [{ id: 10 }] },
      { id: 2, repositories: [{ id: 20 }, { id: 21 }] },
    ]);

    const repositories = await loadIssueSyncLinkedRepositories(4);

    expect(workspaceSCM.getConnectionsOverview).toHaveBeenCalledOnce();
    expect(workspaceSCM.getConnectionsOverview).toHaveBeenCalledWith(4, {
      includeRepositories: true,
    });
    expect(workspaceSCM.getConnections).not.toHaveBeenCalled();
    expect(workspaceSCM.getLinkedRepos).not.toHaveBeenCalled();
    expect(repositories).toEqual([{ id: 10 }, { id: 20 }, { id: 21 }]);
  });

  it('loads setup concurrently from the enriched list and shared workspace snapshot', async () => {
    issueSync.getConfig.mockResolvedValue({ id: 8 });
    workspaceSCM.getConnectionsOverview.mockResolvedValue([{ repositories: [{ id: 10 }] }]);
    const referenceStore = {
      itemTypes: [{ id: 1 }],
      priorities: [{ id: 2 }],
      users: [{ id: 3 }],
      milestones: [{ id: 4 }],
      initialize: vi.fn().mockResolvedValue(undefined),
    };

    const data = await loadIssueSyncPageData(referenceStore, 4);

    expect(issueSync.getConfig).toHaveBeenCalledWith(4);
    expect(workspaceSCM.getConnectionsOverview).toHaveBeenCalledOnce();
    expect(referenceStore.initialize).toHaveBeenCalledWith(4);
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
