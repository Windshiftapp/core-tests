import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api/milestones.js', () => ({
  milestones: { getTestStatisticsMany: vi.fn(), getTestStatistics: vi.fn() },
  milestoneCategories: { getAll: vi.fn().mockResolvedValue([]) },
}));

import { milestones } from '../../api/milestones.js';
import { loadMilestoneTestStatistics } from './milestoneStatisticsData.js';

describe('milestone test-statistics request graph', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('loads every milestone through one batch request', async () => {
    milestones.getTestStatisticsMany.mockResolvedValue({
      1: { total_test_plans: 2 },
      2: { total_test_plans: 3 },
    });

    const statistics = await loadMilestoneTestStatistics([{ id: 1 }, { id: 2 }, { id: 1 }]);

    expect(milestones.getTestStatisticsMany).toHaveBeenCalledOnce();
    expect(milestones.getTestStatisticsMany).toHaveBeenCalledWith([1, 2]);
    expect(milestones.getTestStatistics).not.toHaveBeenCalled();
    expect(statistics).toEqual({
      1: { total_test_plans: 2 },
      2: { total_test_plans: 3 },
    });
  });

  it('does not request an empty milestone set', async () => {
    await expect(loadMilestoneTestStatistics([])).resolves.toEqual({});
    expect(milestones.getTestStatisticsMany).not.toHaveBeenCalled();
  });
});
