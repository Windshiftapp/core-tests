import { describe, expect, test } from 'vitest';

import { buildRRule, parseRRule, rruleToText } from './rruleUtils.js';

describe('recurrence end conditions', () => {
  test('round-trips an after-occurrences COUNT clause', () => {
    const parsed = parseRRule('FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,FR;COUNT=7');

    expect(parsed).toMatchObject({
      frequency: 'WEEKLY',
      interval: 2,
      byDay: ['MO', 'FR'],
      endType: 'count',
      count: 7,
    });
    expect(buildRRule(parsed)).toBe('FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,FR;COUNT=7');
    expect(rruleToText(buildRRule(parsed))).toContain('for 7 occurrences');
  });

  test.each([
    ['date-only', 'UNTIL=20260815'],
    ['UTC date-time', 'UNTIL=20260815T235959Z'],
  ])('round-trips an on-date UNTIL clause from a %s value', (_, untilClause) => {
    const parsed = parseRRule(`FREQ=DAILY;${untilClause}`);

    expect(parsed).toMatchObject({
      frequency: 'DAILY',
      endType: 'date',
      endDate: '2026-08-15',
    });
    expect(buildRRule(parsed)).toBe('FREQ=DAILY;UNTIL=20260815');
    expect(rruleToText(buildRRule(parsed))).toContain('until 2026-08-15');
  });

  test('emits only the selected ending condition', () => {
    expect(
      buildRRule({
        frequency: 'DAILY',
        interval: 1,
        endType: 'count',
        count: 4,
        endDate: '2026-08-15',
      })
    ).toBe('FREQ=DAILY;COUNT=4');

    expect(
      buildRRule({
        frequency: 'DAILY',
        interval: 1,
        endType: 'date',
        count: 4,
        endDate: '2026-08-15',
      })
    ).toBe('FREQ=DAILY;UNTIL=20260815');
  });
});
