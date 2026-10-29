import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api/oncall.js', () => ({
  onCallSchedules: { listForTeam: vi.fn(), get: vi.fn(), getCurrent: vi.fn() },
}));

import { onCallSchedules } from '../api/oncall.js';
import { loadTeamOnCallOverview } from './onCallOverviewData.js';

describe('on-call team overview request graph', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('loads all schedule details and current assignments with one request', async () => {
    onCallSchedules.listForTeam.mockResolvedValue([
      {
        id: 4,
        layers: [{ id: 10, members: [{ user_id: 7 }] }],
        current_on_call: { schedule_id: 4, on_call: [{ user_id: 7 }] },
      },
      { id: 5, layers: [], current_on_call: { schedule_id: 5, on_call: [] } },
    ]);

    const overview = await loadTeamOnCallOverview(3);

    expect(onCallSchedules.listForTeam).toHaveBeenCalledOnce();
    expect(onCallSchedules.listForTeam).toHaveBeenCalledWith(3);
    expect(onCallSchedules.get).not.toHaveBeenCalled();
    expect(onCallSchedules.getCurrent).not.toHaveBeenCalled();
    expect(overview.schedules).toHaveLength(2);
    expect(overview.currentByScheduleId.get(4)).toEqual({
      schedule_id: 4,
      on_call: [{ user_id: 7 }],
    });
    expect(overview.currentByScheduleId.get(5)).toEqual({ schedule_id: 5, on_call: [] });
  });

  it('normalizes an invalid response to an empty overview', async () => {
    onCallSchedules.listForTeam.mockResolvedValue(null);

    const overview = await loadTeamOnCallOverview(3);

    expect(overview.schedules).toEqual([]);
    expect(overview.currentByScheduleId.size).toBe(0);
  });
});
