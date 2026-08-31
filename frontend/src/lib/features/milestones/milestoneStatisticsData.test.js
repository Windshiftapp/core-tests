import { describe, expect, it, vi } from 'vitest';
import { loadMilestoneTestStatistics } from './milestoneStatisticsData.js';

describe('milestone test-statistics request graph', () => {
  it('loads every milestone through one batch request', async () => {
    const apiClient = {
      milestones: {
        getTestStatisticsMany: vi.fn().mockResolvedValue({
          1: { total_test_plans: 2 },
          2: { total_test_plans: 3 },
        }),
        getTestStatistics: vi.fn(),
      },
    };

    const statistics = await loadMilestoneTestStatistics(apiClient, [
      { id: 1 },
      { id: 2 },
      { id: 1 },
    ]);

    expect(apiClient.milestones.getTestStatisticsMany).toHaveBeenCalledOnce();
    expect(apiClient.milestones.getTestStatisticsMany).toHaveBeenCalledWith([1, 2]);
    expect(apiClient.milestones.getTestStatistics).not.toHaveBeenCalled();
    expect(statistics).toEqual({
      1: { total_test_plans: 2 },
      2: { total_test_plans: 3 },
    });
  });

  it('does not request an empty milestone set', async () => {
    const apiClient = {
      milestones: { getTestStatisticsMany: vi.fn() },
    };

    await expect(loadMilestoneTestStatistics(apiClient, [])).resolves.toEqual({});
    expect(apiClient.milestones.getTestStatisticsMany).not.toHaveBeenCalled();
  });
});
