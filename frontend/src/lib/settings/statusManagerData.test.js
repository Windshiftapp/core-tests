import { describe, expect, it, vi } from 'vitest';
import { loadStatusManagerData } from './statusManagerData.js';

describe('status manager transition request graph', () => {
  it('loads all workflow transitions with three bounded requests', async () => {
    const apiClient = {
      statusCategories: { getAll: vi.fn().mockResolvedValue([{ id: 1 }]) },
      statuses: {
        getAll: vi.fn().mockResolvedValue([{ id: 10 }, { id: 11 }, { id: 12 }]),
      },
      workflows: {
        getAllWithTransitions: vi.fn().mockResolvedValue([
          {
            id: 20,
            transitions: [
              { id: 30, from_status_id: 10, to_status_id: 11 },
              { id: 31, from_status_id: 10, to_status_id: 10 },
            ],
          },
          { id: 21, transitions: [{ id: 32, from_status_id: null, to_status_id: 12 }] },
        ]),
        getTransitions: vi.fn(),
      },
    };

    const loading = loadStatusManagerData(apiClient);

    expect(apiClient.statusCategories.getAll).toHaveBeenCalledOnce();
    expect(apiClient.statuses.getAll).toHaveBeenCalledOnce();
    expect(apiClient.workflows.getAllWithTransitions).toHaveBeenCalledOnce();
    expect(apiClient.workflows.getTransitions).not.toHaveBeenCalled();
    const data = await loading;
    expect(data.workflowTransitions).toHaveLength(3);
    expect(data.statuses).toEqual([
      { id: 10, transitionCount: 2 },
      { id: 11, transitionCount: 1 },
      { id: 12, transitionCount: 1 },
    ]);
  });
});
