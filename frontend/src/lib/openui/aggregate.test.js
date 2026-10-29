import { describe, expect, it } from 'vitest';

import { bucketByDate, bucketStart, expandMultiValue, groupCount, groupRows, groupSum } from './aggregate.js';

const rows = [
  { id: 1, status: 'Open', assignee: 'Ada', story_points: 3, created_at: '2026-11-02T09:00:00Z' },
  { id: 2, status: 'Open', assignee: 'Bob', story_points: 5, created_at: '2026-11-03T09:00:00Z' },
  { id: 3, status: 'Done', assignee: 'Ada', story_points: 2, created_at: '2026-11-10T09:00:00Z' },
  { id: 4, status: null, assignee: null, story_points: null, created_at: null },
];

describe('aggregate', () => {
  it('counts rows per group, sorted by value', () => {
    expect(groupCount(rows, 'status')).toEqual([
      { label: 'Open', value: 2 },
      { label: 'Done', value: 1 },
      { label: 'Unknown', value: 1 },
    ]);
  });

  it('honours a group limit', () => {
    expect(groupCount(rows, 'status', { limit: 1 })).toEqual([{ label: 'Open', value: 2 }]);
  });

  it('sums a numeric field per group', () => {
    expect(groupSum(rows, 'assignee', 'story_points')).toEqual([
      { label: 'Ada', value: 5 },
      { label: 'Bob', value: 5 },
      { label: 'Unknown', value: 0 },
    ]);
  });

  it('buckets rows by date in ascending order', () => {
    const buckets = bucketByDate(rows, 'created_at', 'week');
    expect(buckets.map((bucket) => bucket.value)).toEqual([2, 1]);
    expect(buckets[0].label < buckets[1].label).toBe(true);
  });

  it('buckets by month', () => {
    const buckets = bucketByDate(rows, 'created_at', 'month');
    expect(buckets).toHaveLength(1);
    expect(buckets[0].value).toBe(3);
  });

  it('snaps dates to the start of their bucket', () => {
    const monday = bucketStart(new Date(2026, 10, 4, 15, 30), 'week');
    expect(monday.getDate()).toBe(2);
    expect(bucketStart(new Date(2026, 10, 20), 'month').getDate()).toBe(1);
  });

  it('expands multi-valued fields into one entry per value', () => {
    const expanded = expandMultiValue([{ id: 1, linked_items: ['a', 'b'] }], 'linked_items');
    expect(expanded).toEqual([
      { id: 1, linked_items: ['a', 'b'], __group: 'a' },
      { id: 1, linked_items: ['a', 'b'], __group: 'b' },
    ]);
  });

  it('groups through linked items with fanout and primary modes', () => {
    const assets = [
      { id: 1, linked_items: [{ creator: 'Ada' }, { creator: 'Bob' }] },
      { id: 2, linked_items: [{ creator: 'Ada' }] },
      { id: 3, linked_items: [] },
    ];

    expect(groupRows(assets, { groupBy: 'linked_items.creator' })).toEqual([
      { label: 'Ada', value: 2 },
      { label: 'Bob', value: 1 },
      { label: 'Unknown', value: 1 },
    ]);
    expect(groupRows(assets, { groupBy: 'linked_items.creator', mode: 'primary' })).toEqual([
      { label: 'Ada', value: 2 },
      { label: 'Unknown', value: 1 },
    ]);
  });

  it('sums a numeric field through linked items', () => {
    const assets = [
      { id: 1, story_points: 3, linked_items: [{ creator: 'Ada' }, { creator: 'Bob' }] },
      { id: 2, story_points: 5, linked_items: [{ creator: 'Ada' }] },
    ];
    expect(
      groupRows(assets, { groupBy: 'linked_items.creator', metric: 'sum', field: 'story_points' })
    ).toEqual([
      { label: 'Ada', value: 8 },
      { label: 'Bob', value: 3 },
    ]);
  });

  it('falls back to plain grouping for non-linked fields', () => {
    expect(groupRows(rows, { groupBy: 'status' })).toEqual(groupCount(rows, 'status'));
  });

  it('buckets in local time, not UTC', () => {
    // Late in the local day: a UTC-based bucket would push this into the next
    // day for every timezone east of Greenwich.
    const late = new Date(2026, 10, 4, 23, 30);
    const buckets = bucketByDate([{ created_at: late.toISOString() }], 'created_at', 'day');

    expect(buckets).toHaveLength(1);
    const start = new Date(buckets[0].label);
    expect(start.getFullYear()).toBe(2026);
    expect(start.getMonth()).toBe(10);
    expect(start.getDate()).toBe(4);
    expect(start.getHours()).toBe(0);
  });

  it('tolerates empty input', () => {
    expect(groupCount([], 'status')).toEqual([]);
    expect(bucketByDate([], 'created_at')).toEqual([]);
  });
});
