import { describe, expect, it } from 'vitest';
import {
  defaultAnalyticsRange,
  formatDateOnly,
  inclusiveDateRangeDays,
  shiftDateString,
  validateAnalyticsRange,
} from './analyticsView.js';

describe('analytics date helpers', () => {
  it('builds an inclusive twelve-week default from the local calendar date', () => {
    const range = defaultAnalyticsRange(new Date(2026, 6, 17, 23, 30));
    expect(range).toEqual({ startDate: '2026-04-25', endDate: '2026-07-17' });
    expect(inclusiveDateRangeDays(range.startDate, range.endDate)).toBe(84);
  });

  it('shifts date-only strings without crossing through local time', () => {
    expect(shiftDateString('2026-03-01', -1)).toBe('2026-02-28');
    expect(shiftDateString('2024-03-01', -1)).toBe('2024-02-29');
  });

  it('renders date-only values on the requested calendar day in negative UTC offsets', () => {
    const previousTimezone = process.env.TZ;
    process.env.TZ = 'America/Los_Angeles';
    try {
      const rendered = formatDateOnly('2026-07-17', {
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
      });
      expect(rendered).toContain('17');
      expect(rendered).not.toContain('16');
    } finally {
      if (previousTimezone === undefined) {
        delete process.env.TZ;
      } else {
        process.env.TZ = previousTimezone;
      }
    }
  });

  it('rejects invalid, reversed, and unbounded ranges', () => {
    expect(validateAnalyticsRange('2026-02-30', '2026-03-01')).toBe('invalid');
    expect(validateAnalyticsRange('2026-03-02', '2026-03-01')).toBe('reversed');
    expect(validateAnalyticsRange('2025-01-01', '2026-01-02')).toBe('too_long');
    expect(validateAnalyticsRange('2026-01-01', '2026-12-31')).toBeNull();
  });
});
