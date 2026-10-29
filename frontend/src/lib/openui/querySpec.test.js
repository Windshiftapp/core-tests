import { describe, expect, it } from 'vitest';

import {
  DEFAULT_LIMIT,
  MAX_LIMIT,
  assetSpec,
  fieldValue,
  formatValue,
  itemSpec,
  linksSpec,
  parseColumns,
  sourceKey,
} from './querySpec.js';

describe('query spec', () => {
  it('prunes empty values so equivalent slots share a key', () => {
    expect(itemSpec({ query: '', sort: '', limit: DEFAULT_LIMIT })).toEqual({
      entity: 'item',
      limit: DEFAULT_LIMIT,
    });
    expect(sourceKey(itemSpec({ query: 'a = 1' }))).toBe(
      sourceKey(itemSpec({ limit: DEFAULT_LIMIT, query: 'a = 1' }))
    );
  });

  it('distinguishes different queries, limits, and entity types', () => {
    const base = sourceKey(itemSpec({ query: 'a = 1' }));
    expect(sourceKey(itemSpec({ query: 'a = 2' }))).not.toBe(base);
    expect(sourceKey(itemSpec({ query: 'a = 1', limit: 5 }))).not.toBe(base);
    expect(sourceKey(assetSpec({ query: 'a = 1', assetSetId: 3 }))).not.toBe(base);
  });

  it('keeps the asset set in the spec', () => {
    expect(assetSpec({ query: '', assetSetId: 9 })).toEqual({
      entity: 'asset',
      limit: DEFAULT_LIMIT,
      asset_set_id: 9,
    });
  });

  it('resolves dotted field paths and object labels', () => {
    const row = { status: { id: 3, name: 'Open' }, assignee: null, story_points: 5 };
    expect(fieldValue(row, 'status')).toBe('Open');
    expect(fieldValue(row, 'status.id')).toBe(3);
    expect(fieldValue(row, 'assignee.name')).toBeUndefined();
    expect(fieldValue(row, 'story_points')).toBe(5);
  });

  it('formats values for display', () => {
    expect(formatValue(null)).toBe('—');
    expect(formatValue(true)).toBe('Yes');
    expect(formatValue(0)).toBe('0');
    expect(formatValue('2026-11-01T10:00:00Z')).not.toBe('2026-11-01T10:00:00Z');
  });

  it('builds an edge slot with both seed sets', () => {
    const spec = linksSpec({
      itemQuery: 'status_completed = false',
      itemSort: 'updated_at',
      itemLimit: 500,
      assetQuery: 'status = "Active"',
      assetSetId: 7,
      maxEdges: 250,
    });

    expect(spec).toEqual({
      entity: 'links',
      links: {
        item: { ql: 'status_completed = false', sort: 'updated_at', limit: 500 },
        asset: { ql: 'status = "Active"', asset_set_id: 7, limit: MAX_LIMIT },
        max_edges: 250,
      },
    });
  });

  it('keeps edge slots distinct from row slots and from each other', () => {
    const base = sourceKey(linksSpec({ itemQuery: 'a = 1' }));
    expect(base).not.toBe(sourceKey(itemSpec({ query: 'a = 1' })));
    expect(sourceKey(linksSpec({ itemQuery: 'a = 2' }))).not.toBe(base);
    expect(sourceKey(linksSpec({ itemQuery: 'a = 1', maxEdges: 5 }))).not.toBe(base);
  });

  it('parses a column list', () => {
    expect(parseColumns(' key, title ,status ')).toEqual(['key', 'title', 'status']);
    expect(parseColumns(undefined)).toEqual([]);
  });
});
