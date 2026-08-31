import { describe, expect, it, vi } from 'vitest';
import { loadTeamOnCallOverview } from './onCallOverviewData.js';

describe('on-call team overview request graph', () => {
  it('loads all schedule details and current assignments with one request', async () => {
    const apiClient = {
      onCallSchedules: {
        listForTeam: vi.fn().mockResolvedValue([
          {
            id: 4,
            layers: [{ id: 10, members: [{ user_id: 7 }] }],
            current_on_call: { schedule_id: 4, on_call: [{ user_id: 7 }] },
          },
          { id: 5, layers: [], current_on_call: { schedule_id: 5, on_call: [] } },
        ]),
        get: vi.fn(),
        getCurrent: vi.fn(),
      },
    };

    const overview = await loadTeamOnCallOverview(apiClient, 3);

    expect(apiClient.onCallSchedules.listForTeam).toHaveBeenCalledOnce();
    expect(apiClient.onCallSchedules.listForTeam).toHaveBeenCalledWith(3);
    expect(apiClient.onCallSchedules.get).not.toHaveBeenCalled();
    expect(apiClient.onCallSchedules.getCurrent).not.toHaveBeenCalled();
    expect(overview.schedules).toHaveLength(2);
    expect(overview.currentByScheduleId.get(4)).toEqual({
      schedule_id: 4,
      on_call: [{ user_id: 7 }],
    });
    expect(overview.currentByScheduleId.get(5)).toEqual({ schedule_id: 5, on_call: [] });
  });

  it('normalizes an invalid response to an empty overview', async () => {
    const apiClient = {
      onCallSchedules: { listForTeam: vi.fn().mockResolvedValue(null) },
    };

    const overview = await loadTeamOnCallOverview(apiClient, 3);

    expect(overview.schedules).toEqual([]);
    expect(overview.currentByScheduleId.size).toBe(0);
  });
});
