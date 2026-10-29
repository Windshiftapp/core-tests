import { describe, expect, it } from 'vitest';
import { sortBacklogIterations } from './backlogIterationOrder.js';

describe('sortBacklogIterations', () => {
  it('groups active first, then planned, then completed/cancelled', () => {
    const sorted = sortBacklogIterations([
      { id: 1, name: 'Completed', status: 'completed', start_date: '2026-01-01' },
      { id: 2, name: 'Planned', status: 'planned', start_date: '2026-01-01' },
      { id: 3, name: 'Active', status: 'active', start_date: '2026-01-01' },
      { id: 4, name: 'Cancelled', status: 'cancelled', start_date: '2026-01-01' },
    ]);

    expect(sorted.map((iteration) => iteration.status)).toEqual([
      'active',
      'planned',
      'completed',
      'cancelled',
    ]);
  });

  it('puts the newest start_date first inside a status group', () => {
    const sorted = sortBacklogIterations([
      { id: 1, name: 'Old', status: 'planned', start_date: '2026-01-01' },
      { id: 2, name: 'New', status: 'planned', start_date: '2026-06-01' },
      { id: 3, name: 'Middle', status: 'planned', start_date: '2026-03-01' },
    ]);

    expect(sorted.map((iteration) => iteration.name)).toEqual(['New', 'Middle', 'Old']);
  });

  it('orders equal start_dates by newest created instead of name', () => {
    const sorted = sortBacklogIterations([
      { id: 10, name: 'Alpha', status: 'planned', start_date: '2026-01-01' },
      { id: 12, name: 'Zulu', status: 'planned', start_date: '2026-01-01' },
      { id: 11, name: 'Bravo', status: 'planned', start_date: '2026-01-01' },
    ]);

    expect(sorted.map((iteration) => iteration.name)).toEqual(['Zulu', 'Bravo', 'Alpha']);
  });

  it('keeps local and global iterations in one newest-first order', () => {
    const sorted = sortBacklogIterations([
      { id: 1, name: 'Local older', status: 'planned', start_date: '2026-01-01' },
      { id: 2, name: 'Global newer', status: 'planned', start_date: '2026-05-01', is_global: true },
    ]);

    expect(sorted.map((iteration) => iteration.name)).toEqual([
      'Global newer',
      'Local older',
    ]);
  });

  it('does not mutate the input array', () => {
    const input = [
      { id: 1, status: 'planned', start_date: '2026-01-01' },
      { id: 2, status: 'active', start_date: '2026-01-01' },
    ];
    const before = [...input];

    sortBacklogIterations(input);

    expect(input).toEqual(before);
  });
});
