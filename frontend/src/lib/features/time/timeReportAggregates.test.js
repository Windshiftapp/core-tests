import { describe, expect, test } from 'vitest';

import { buildSummary, buildMemberBreakdown, buildDailyChartData } from './timeReportAggregates.js';

const totals = [
  { user_id: 1, user_name: 'Ada', project_id: 10, project_name: 'Apollo', customer_id: 100, customer_name: 'Acme', duration_minutes: 600, entries: 5 },
  { user_id: 1, user_name: 'Ada', project_id: 11, project_name: 'Zephyr', customer_id: 101, customer_name: 'Globex', duration_minutes: 120, entries: 1 },
  { user_id: 2, user_name: 'Ben', project_id: 10, project_name: 'Apollo', customer_id: 100, customer_name: 'Acme', duration_minutes: 240, entries: 2 },
];

const daily = [
  { day: '2026-09-01', user_id: 1, user_name: 'Ada', minutes: 300 },
  { day: '2026-09-02', user_id: 1, user_name: 'Ada', minutes: 420 },
  { day: '2026-09-01', user_id: 2, user_name: 'Ben', minutes: 240 },
];

describe('buildSummary', () => {
  test('sums duration and entries and ranks top project/customer', () => {
    const summary = buildSummary(totals, { dateFrom: '2026-09-01', dateTo: '2026-09-10' });

    expect(summary.totalHours).toBe(16);
    expect(summary.totalEntries).toBe(8);
    expect(summary.topProject).toEqual({ name: 'Apollo', hours: 14 });
    expect(summary.topCustomer).toEqual({ name: 'Acme', hours: 14 });
    expect(summary.averageHoursPerDay).toBe(16 / 10);
  });

  test('handles empty aggregates', () => {
    const summary = buildSummary([]);
    expect(summary).toEqual({
      totalHours: 0, totalEntries: 0, averageHoursPerDay: 0, topProject: null, topCustomer: null,
    });
  });

  test('carries the builtin key of the top customer so the UI can localize it', () => {
    const summary = buildSummary([
      { user_id: 1, user_name: 'Ada', project_id: 10, project_name: 'Apollo', customer_id: 100, customer_name: 'No customer', customer_builtin_key: 'no_customer', duration_minutes: 600, entries: 5 },
    ]);

    expect(summary.topCustomer).toEqual({ name: 'No customer', hours: 10, builtinKey: 'no_customer' });
  });
});

describe('buildMemberBreakdown', () => {
  test('merges per-user totals with distinct active days from the daily split', () => {
    const breakdown = buildMemberBreakdown(daily, totals);

    expect(breakdown).toEqual([
      { user_name: 'Ada', hours: 12, entries: 6, avgPerDay: 6 },
      { user_name: 'Ben', hours: 4, entries: 2, avgPerDay: 4 },
    ]);
  });

  test('is empty without totals', () => {
    expect(buildMemberBreakdown(daily, [])).toEqual([]);
  });
});

describe('buildDailyChartData', () => {
  test('collapses day-split groups into a sorted series of hours', () => {
    const chart = buildDailyChartData([
      ...daily,
      { day: '2026-09-01', user_id: 3, user_name: 'Cyd', minutes: 60 },
    ]);

    expect(chart.map((d) => d.label)).toEqual(['2026-09-01', '2026-09-02']);
    expect(chart[0].count).toBe(10);
    expect(chart[1].count).toBe(7);
  });

  test('is empty without daily groups', () => {
    expect(buildDailyChartData([])).toEqual([]);
  });
});
