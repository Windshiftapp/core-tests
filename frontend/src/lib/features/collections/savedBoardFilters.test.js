import { describe, expect, it } from 'vitest';
import {
  addSavedFilter,
  orderedSavedFilters,
  parseSavedFilters,
  removeSavedFilter,
  savedFiltersStorageKey,
  serializeSavedFilters,
  togglePinnedSavedFilter,
} from './savedBoardFilters.js';

describe('saved board filters', () => {
  it('scopes storage keys to the collection when present, otherwise the workspace', () => {
    expect(savedFiltersStorageKey(4, null)).toBe('board-saved-filters:workspace-4');
    expect(savedFiltersStorageKey(4, 9)).toBe('board-saved-filters:collection-9');
  });

  it('round-trips named filter sets including pinned state and rows', () => {
    const rows = [{ field: 'priority', operator: '=', value: 'high' }];
    const serialized = serializeSavedFilters([
      { id: 'a', name: 'High priority', ql: 'priority = high', rows, pinned: true },
    ]);

    expect(parseSavedFilters(serialized)).toEqual([
      { id: 'a', name: 'High priority', ql: 'priority = high', rows, pinned: true },
    ]);
  });

  it('drops malformed or queryless entries instead of failing the whole list', () => {
    const raw = JSON.stringify([
      { id: 'ok', name: 'Valid', ql: 'status = open', rows: [], pinned: false },
      { id: 'no-ql', name: 'Missing query' },
      'not-an-object',
      { name: 'No query' },
    ]);

    expect(parseSavedFilters(raw).map((entry) => entry.id)).toEqual(['ok']);
    expect(parseSavedFilters('{not json')).toEqual([]);
    expect(parseSavedFilters(null)).toEqual([]);
  });

  it('rejects empty, over-long, and duplicate names', () => {
    const existing = [{ id: 'a', name: 'Mine', ql: 'status = open', rows: [], pinned: false }];

    expect(addSavedFilter(existing, { name: '  ', ql: 'x' }).error).toBe('name_required');
    expect(addSavedFilter(existing, { name: 'x'.repeat(61), ql: 'x' }).error).toBe('name_too_long');
    expect(addSavedFilter(existing, { name: 'mine', ql: 'x' }).error).toBe('duplicate_name');
    expect(addSavedFilter(existing, { name: 'New', ql: '' }).error).toBe('filter_required');
  });

  it('adds a filter with a stable id and no pinned state by default', () => {
    const result = addSavedFilter(
      [],
      { name: '  Mine  ', ql: 'status = open', rows: [] },
      () => 'generated'
    );

    expect(result.error).toBeNull();
    expect(result.filters).toEqual([
      { id: 'generated', name: 'Mine', ql: 'status = open', rows: [], pinned: false },
    ]);
  });

  it('removes filters by id and toggles pinning', () => {
    const filters = [
      { id: 'a', name: 'A', ql: 'a', rows: [], pinned: false },
      { id: 'b', name: 'B', ql: 'b', rows: [], pinned: false },
    ];

    expect(removeSavedFilter(filters, 'a').map((entry) => entry.id)).toEqual(['b']);
    expect(togglePinnedSavedFilter(filters, 'b')[1].pinned).toBe(true);
    expect(togglePinnedSavedFilter(togglePinnedSavedFilter(filters, 'b'), 'b')[1].pinned).toBe(false);
  });

  it('orders pinned filters first', () => {
    const filters = [
      { id: 'a', name: 'A', ql: 'a', rows: [], pinned: false },
      { id: 'b', name: 'B', ql: 'b', rows: [], pinned: true },
      { id: 'c', name: 'C', ql: 'c', rows: [], pinned: false },
    ];

    expect(orderedSavedFilters(filters).map((entry) => entry.id)).toEqual(['b', 'a', 'c']);
  });
});
