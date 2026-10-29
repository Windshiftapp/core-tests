import { beforeEach, describe, expect, it, vi } from 'vitest';

const core = vi.hoisted(() => ({
  fetchAPIV2: vi.fn(),
  fetchV2Data: vi.fn(),
  fetchAllV2Pages: vi.fn(),
}));

vi.mock('./core.js', () => core);

import { time } from './time.js';

describe('time worklog aggregate (WI-1582)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('reads the bare aggregate response without unwrapping a data envelope', async () => {
    const aggregate = {
      totals: { duration_ms: 42, entry_count: 1 },
      daily: [{ date: '2026-01-01', duration_ms: 42 }],
    };
    core.fetchAPIV2.mockResolvedValue(aggregate);

    const result = await time.worklogs.aggregate({
      date_from: '2026-01-01',
      date_to: '2026-01-31',
    });

    // The endpoint writes a bare WorklogAggregate, so the client must use the
    // unwrapped fetch (fetchAPIV2) rather than fetchV2Data's document.data.
    expect(core.fetchAPIV2).toHaveBeenCalledTimes(1);
    expect(core.fetchAPIV2.mock.calls[0][0]).toBe(
      '/time/worklogs/aggregate?from=2026-01-01&to=2026-01-31'
    );
    expect(core.fetchV2Data).not.toHaveBeenCalled();
    expect(result).toBe(aggregate);
  });
});
