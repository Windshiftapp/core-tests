import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api/workflows.js', () => ({
  statusCategories: { getAll: vi.fn() },
  statuses: { getAll: vi.fn() },
  workflows: { getAllWithTransitions: vi.fn(), getTransitions: vi.fn() },
}));

import { statusCategories, statuses, workflows } from '../api/workflows.js';
import { loadStatusManagerData } from './statusManagerData.js';

describe('status manager transition request graph', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('loads all workflow transitions with three bounded requests', async () => {
    statusCategories.getAll.mockResolvedValue([{ id: 1 }]);
    statuses.getAll.mockResolvedValue([{ id: 10 }, { id: 11 }, { id: 12 }]);
    workflows.getAllWithTransitions.mockResolvedValue([
      {
        id: 20,
        transitions: [
          { id: 30, from_status_id: 10, to_status_id: 11 },
          { id: 31, from_status_id: 10, to_status_id: 10 },
        ],
      },
      { id: 21, transitions: [{ id: 32, from_status_id: null, to_status_id: 12 }] },
    ]);

    const loading = loadStatusManagerData();

    expect(statusCategories.getAll).toHaveBeenCalledOnce();
    expect(statuses.getAll).toHaveBeenCalledOnce();
    expect(workflows.getAllWithTransitions).toHaveBeenCalledOnce();
    expect(workflows.getTransitions).not.toHaveBeenCalled();
    const data = await loading;
    expect(data.workflowTransitions).toHaveLength(3);
    expect(data.statuses).toEqual([
      { id: 10, transitionCount: 2 },
      { id: 11, transitionCount: 1 },
      { id: 12, transitionCount: 1 },
    ]);
  });
});
