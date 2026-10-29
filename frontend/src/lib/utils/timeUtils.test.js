import { afterEach, describe, expect, test, vi } from 'vitest';
import { addMinutesToTime, createDurationSync, durationToString, listIanaTimezones, minutesBetweenTimes, parseDuration } from './timeUtils.js';

describe('listIanaTimezones', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  test('includes South American zones so Brazilian users can pick their region', () => {
    const zones = listIanaTimezones();
    expect(zones).toContain('America/Sao_Paulo');
    expect(zones.length).toBeGreaterThan(20);
  });

  test('falls back to a curated set containing America/Sao_Paulo when unsupported', () => {
    vi.stubGlobal('Intl', { ...Intl, supportedValuesOf: undefined });

    const zones = listIanaTimezones();
    expect(zones).toContain('America/Sao_Paulo');
  });

  test('falls back when supportedValuesOf throws', () => {
    vi.stubGlobal('Intl', {
      ...Intl,
      supportedValuesOf: () => {
        throw new Error('unsupported');
      },
    });

    expect(listIanaTimezones()).toContain('UTC');
  });
});

describe('parseDuration', () => {
  test('returns 0 for empty or null input', () => {
    expect(parseDuration('')).toBe(0);
    expect(parseDuration(null)).toBe(0);
    expect(parseDuration(undefined)).toBe(0);
  });

  test('parses hour-only durations', () => {
    expect(parseDuration('2h')).toBe(120);
    expect(parseDuration('1h')).toBe(60);
    expect(parseDuration('0.5h')).toBe(30);
  });

  test('parses minute-only durations', () => {
    expect(parseDuration('30m')).toBe(30);
    expect(parseDuration('90m')).toBe(90);
  });

  test('parses combined hour+minute durations', () => {
    expect(parseDuration('2h30m')).toBe(150);
    expect(parseDuration('1h45m')).toBe(105);
  });

  test('parses days using hoursPerDay (default 8)', () => {
    expect(parseDuration('1d')).toBe(480); // 8 * 60
    expect(parseDuration('2d')).toBe(960);
    expect(parseDuration('0.5d')).toBe(240);
  });

  test('honors custom hoursPerDay for day parsing', () => {
    expect(parseDuration('1d', 6)).toBe(360);
    expect(parseDuration('2d', 10)).toBe(1200);
  });

  test('is case-insensitive and tolerates whitespace', () => {
    expect(parseDuration(' 2H ')).toBe(120);
    expect(parseDuration('1H30M')).toBe(90);
  });
});

describe('durationToString', () => {
  test('formats minutes-only', () => {
    expect(durationToString(30)).toBe('30m');
    expect(durationToString(1)).toBe('1m');
    expect(durationToString(59)).toBe('59m');
  });

  test('formats hours-only', () => {
    expect(durationToString(60)).toBe('1h');
    expect(durationToString(120)).toBe('2h');
  });

  test('formats hours and minutes combined', () => {
    expect(durationToString(90)).toBe('1h30m');
    expect(durationToString(125)).toBe('2h5m');
  });

  test('clamps negative values to 0m', () => {
    expect(durationToString(-10)).toBe('0m');
    expect(durationToString(-1)).toBe('0m');
  });

  test('rounds fractional minutes', () => {
    expect(durationToString(30.4)).toBe('30m');
    expect(durationToString(30.6)).toBe('31m');
  });
});

describe('addMinutesToTime', () => {
  test('returns empty string for empty input', () => {
    expect(addMinutesToTime('', 30)).toBe('');
    expect(addMinutesToTime(null, 30)).toBe('');
  });

  test('adds minutes within the same hour', () => {
    expect(addMinutesToTime('09:00', 15)).toBe('09:15');
    expect(addMinutesToTime('14:30', 25)).toBe('14:55');
  });

  test('rolls over the hour', () => {
    expect(addMinutesToTime('09:45', 30)).toBe('10:15');
    expect(addMinutesToTime('10:30', 90)).toBe('12:00');
  });

  test('accepts negative offsets', () => {
    expect(addMinutesToTime('10:00', -15)).toBe('09:45');
  });
});

describe('minutesBetweenTimes', () => {
  test('returns 0 when either input is missing', () => {
    expect(minutesBetweenTimes('', '10:00')).toBe(0);
    expect(minutesBetweenTimes('09:00', '')).toBe(0);
    expect(minutesBetweenTimes(null, null)).toBe(0);
  });

  test('returns positive minutes when end is after start', () => {
    expect(minutesBetweenTimes('09:00', '10:00')).toBe(60);
    expect(minutesBetweenTimes('09:15', '09:45')).toBe(30);
  });

  test('wraps an end at or before the start to the next day', () => {
    // Overnight entries: 23:00-01:00 is a continuous two-hour interval, and
    // equal clocks describe a full-day entry matching the server-side cap.
    expect(minutesBetweenTimes('23:00', '01:00')).toBe(120);
    expect(minutesBetweenTimes('23:59', '00:00')).toBe(1);
    expect(minutesBetweenTimes('09:00', '09:00')).toBe(24 * 60);
  });
});

describe('createDurationSync', () => {
  test('guard runs the callback once and reports idle afterwards', () => {
    const sync = createDurationSync();
    expect(sync.isUpdating()).toBe(false);

    let ran = 0;
    sync.guard(() => {
      ran++;
      // While the guard is active, isUpdating must report true so a
      // nested guard call is a no-op.
      expect(sync.isUpdating()).toBe(true);
    });
    expect(ran).toBe(1);
    expect(sync.isUpdating()).toBe(false);
  });

  test('nested guard calls are suppressed', () => {
    const sync = createDurationSync();
    let outer = 0;
    let inner = 0;
    sync.guard(() => {
      outer++;
      sync.guard(() => {
        inner++;
      });
    });
    expect(outer).toBe(1);
    expect(inner).toBe(0);
  });

  test('guard resets isUpdating even when callback throws', () => {
    const sync = createDurationSync();
    expect(() => {
      sync.guard(() => {
        throw new Error('boom');
      });
    }).toThrow('boom');
    expect(sync.isUpdating()).toBe(false);
  });
});
