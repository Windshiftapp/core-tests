import { describe, expect, test } from 'vitest';

import {
  defaultRowCount,
  normalizeTaskResponse,
  ROW_COUNT_OPTIONS,
  resolveDensity,
  resolveRowCount,
  rowCountToLimit,
  shouldShowRowControls,
} from './taskWidgetState.js';

describe('defaultRowCount', () => {
  test('returns 6 below half width', () => {
    expect(defaultRowCount(3)).toBe(6);
    expect(defaultRowCount(5)).toBe(6);
  });

  test('returns 10 at or above half width', () => {
    expect(defaultRowCount(6)).toBe(10);
    expect(defaultRowCount(12)).toBe(10);
  });
});

describe('resolveRowCount', () => {
  test('uses explicit override when present', () => {
    expect(resolveRowCount({ rowCount: 15 }, 3)).toBe(15);
    expect(resolveRowCount({ rowCount: 'all' }, 12)).toBe('all');
  });

  test('falls back to width-derived default when no override', () => {
    expect(resolveRowCount({}, 3)).toBe(6);
    expect(resolveRowCount(null, 6)).toBe(10);
    expect(resolveRowCount(undefined, 12)).toBe(10);
  });
});

describe('resolveDensity', () => {
  test('compact when explicitly set', () => {
    expect(resolveDensity({ density: 'compact' })).toBe('compact');
  });

  test('comfortable as default', () => {
    expect(resolveDensity({})).toBe('comfortable');
    expect(resolveDensity(null)).toBe('comfortable');
  });
});

describe('rowCountToLimit', () => {
  test('numeric count passes through with a floor of 5', () => {
    expect(rowCountToLimit(5)).toBe(5);
    expect(rowCountToLimit(10)).toBe(10);
    expect(rowCountToLimit(15)).toBe(15);
  });

  test("'all' maps to a generous cap", () => {
    expect(rowCountToLimit('all')).toBe(100);
  });
});

describe('ROW_COUNT_OPTIONS', () => {
  test('offers 5 / 10 / 15 / all', () => {
    expect(ROW_COUNT_OPTIONS).toEqual([5, 10, 15, 'all']);
  });
});

describe('shouldShowRowControls', () => {
  test('hides Saved Search row controls until a collection is selected', () => {
    expect(shouldShowRowControls('saved-search', {})).toBe(false);
    expect(shouldShowRowControls('saved-search', { collectionId: 12 })).toBe(true);
  });

  test('keeps row controls available for regular task widgets', () => {
    expect(shouldShowRowControls('assigned-to-me', {})).toBe(true);
    expect(shouldShowRowControls('daily-briefing', {})).toBe(false);
  });
});

describe('normalizeTaskResponse', () => {
  const items = [
    { id: 1, due_date: '2026-06-05T00:00:00Z' },
    { id: 2, due_date: null },
    { id: 3, due_date: '2026-06-01T00:00:00Z' },
    { id: 4, due_date: '2026-06-10T00:00:00Z' },
  ];

  test('sorts by due date (earliest first, nulls last)', () => {
    const result = normalizeTaskResponse(items);
    expect(result.map((t) => t.id)).toEqual([3, 1, 4, 2]);
  });

  test('respects numeric maxItems cap', () => {
    const result = normalizeTaskResponse(items, 2);
    expect(result).toHaveLength(2);
    expect(result[0].id).toBe(3); // earliest due date
  });

  test("'all' returns every item", () => {
    const result = normalizeTaskResponse(items, 'all');
    expect(result).toHaveLength(4);
  });

  test('handles wrapped { items: [...] } responses', () => {
    const result = normalizeTaskResponse({ items });
    expect(result).toHaveLength(4);
  });

  test('filters out entries without an id', () => {
    const result = normalizeTaskResponse([{ title: 'no id' }, ...items]);
    expect(result).toHaveLength(4);
  });
});
