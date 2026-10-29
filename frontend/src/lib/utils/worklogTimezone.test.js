import { describe, expect, test, vi } from 'vitest';

vi.mock('../stores/auth.svelte.js', () => ({
  authStore: { currentUser: { timezone: 'America/Denver' } },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  i18n: { locale: 'en-US' },
  t: (key) => key,
}));

import { addDaysToKey, dateKeyInZone, formatClockInZone, mondayKeyInZone, monthBoundsInZone, splitWorklogMinutesByDay } from './worklogTimezone.js';

// 2026-08-06T05:00:00Z = Aug 5, 23:00 Denver and Aug 6, 14:00 Tokyo.
const OVERNIGHT_START = 1785992400;
const OVERNIGHT_END = 1785999600;

describe('dateKeyInZone', () => {
  test('derives the civil date from the reporting timezone', () => {
    expect(dateKeyInZone(OVERNIGHT_START, 'America/Denver')).toBe('2026-08-05');
    expect(dateKeyInZone(OVERNIGHT_START, 'Asia/Tokyo')).toBe('2026-08-06');
  });

  test('returns empty for missing instants', () => {
    expect(dateKeyInZone(undefined, 'UTC')).toBe('');
  });
});

describe('formatClockInZone', () => {
  test('formats the wall clock in the reporting timezone', () => {
    expect(formatClockInZone(OVERNIGHT_START, 'America/Denver')).toBe('23:00');
    expect(formatClockInZone(OVERNIGHT_START, 'Asia/Tokyo')).toBe('14:00');
  });

  test('midnight renders as 00:00', () => {
    expect(formatClockInZone(0, 'UTC')).toBe('00:00');
  });
});

describe('splitWorklogMinutesByDay', () => {
  test('splits an overnight entry at local midnight without losing minutes', () => {
    const split = splitWorklogMinutesByDay(OVERNIGHT_START, OVERNIGHT_END, 'America/Denver');
    expect(Object.fromEntries(split)).toEqual({ '2026-08-05': 60, '2026-08-06': 60 });
  });

  test('an exact-midnight end does not count into the next day', () => {
    // Aug 5, 22:00 - Aug 6, 00:00 Denver.
    const split = splitWorklogMinutesByDay(1785988800, 1785996000, 'America/Denver');
    expect(Object.fromEntries(split)).toEqual({ '2026-08-05': 120 });
  });

  test('the same interval groups by different days in another timezone', () => {
    // Denver Aug 5, 16:00-18:00 appears as Aug 6, 07:00-09:00 Tokyo.
    const split = splitWorklogMinutesByDay(1785967200, 1785974400, 'Asia/Tokyo');
    expect(Object.fromEntries(split)).toEqual({ '2026-08-06': 120 });
  });

  test('keeps elapsed time across a DST boundary', () => {
    // Zurich 2026-03-28 23:00 -> 2026-03-29 04:00 spans the spring-forward
    // night: five wall-clock hours, four elapsed hours.
    const split = splitWorklogMinutesByDay(
      Date.UTC(2026, 2, 28, 22, 0) / 1000,
      Date.UTC(2026, 2, 29, 2, 0) / 1000,
      'Europe/Zurich'
    );
    expect(Object.fromEntries(split)).toEqual({ '2026-03-28': 60, '2026-03-29': 180 });
  });

  test('returns no allocation for invalid intervals', () => {
    expect(splitWorklogMinutesByDay(0, 0, 'UTC').size).toBe(0);
    expect(splitWorklogMinutesByDay(100, 50, 'UTC').size).toBe(0);
  });
});

describe('monthBoundsInZone', () => {
  test('uses the calendar month of the local date', () => {
    // 2026-09-01T00:30:00Z is still Aug 31 in Denver but Sep 1 in Tokyo.
    const instant = new Date('2026-09-01T00:30:00Z');
    expect(monthBoundsInZone('America/Denver', instant)).toEqual({
      from: '2026-08-01',
      to: '2026-08-31',
    });
    expect(monthBoundsInZone('Asia/Tokyo', instant)).toEqual({
      from: '2026-09-01',
      to: '2026-09-30',
    });
  });
});

describe('mondayKeyInZone', () => {
  test('anchors the week to the local Monday', () => {
    // 2026-08-03T03:00:00Z is Sunday Aug 2 evening in Denver but Monday
    // Aug 3 midday in Tokyo.
    const instant = new Date('2026-08-03T03:00:00Z');
    expect(mondayKeyInZone('America/Denver', instant)).toBe('2026-07-27');
    expect(mondayKeyInZone('Asia/Tokyo', instant)).toBe('2026-08-03');
  });
});

describe('addDaysToKey', () => {
  test('shifts across month and year boundaries', () => {
    expect(addDaysToKey('2026-08-31', 1)).toBe('2026-09-01');
    expect(addDaysToKey('2026-03-01', -1)).toBe('2026-02-28');
    expect(addDaysToKey('2026-12-31', 1)).toBe('2027-01-01');
  });
});
