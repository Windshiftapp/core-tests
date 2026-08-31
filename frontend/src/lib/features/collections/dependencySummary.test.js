import { describe, expect, it } from 'vitest';
import { DEPENDS_ON_NAME, dependencyKey, splitDependencies } from './dependencySummary.js';

// Build a link object shaped like the merged outgoing/incoming link payload
// the item-links API returns (see internal/models/item.go ItemLink join).
function link(overrides = {}) {
  return {
    id: 1,
    link_type_id: 3,
    link_type_name: DEPENDS_ON_NAME,
    source_type: 'item',
    source_id: 100,
    target_type: 'item',
    target_id: 200,
    source_title: 'Source Item',
    target_title: 'Target Item',
    source_workspace_key: 'WI',
    target_workspace_key: 'WI',
    source_item_number: 10,
    target_item_number: 20,
    source_status_name: 'Open',
    target_status_name: 'Done',
    source_status_color: '#ccc',
    target_status_color: '#0f0',
    ...overrides,
  };
}

describe('splitDependencies', () => {
  it('treats outgoing "Depends On" links (item is source) as blockers', () => {
    // item 100 depends on item 200 → 200 blocks 100.
    const { blockers, blocking } = splitDependencies([link()], 100);
    expect(blockers).toHaveLength(1);
    expect(blocking).toHaveLength(0);
    expect(blockers[0]).toMatchObject({
      id: 200,
      title: 'Target Item',
      keyPrefix: 'WI',
      itemNumber: 20,
    });
  });

  it('treats incoming "Depends On" links (item is target) as blocking others', () => {
    // item 200 is depended on by item 100 → 200 blocks 100.
    const { blockers, blocking } = splitDependencies([link()], 200);
    expect(blockers).toHaveLength(0);
    expect(blocking).toHaveLength(1);
    expect(blocking[0]).toMatchObject({
      id: 100,
      title: 'Source Item',
      keyPrefix: 'WI',
      itemNumber: 10,
    });
  });

  it('ignores links of other types (Implements, Pages, etc.)', () => {
    const links = [
      link({ id: 2, link_type_name: 'Implements' }),
      link({ id: 3, link_type_name: 'Relates To' }),
      link({ id: 4, link_type_name: 'Page' }),
    ];
    const result = splitDependencies(links, 100);
    expect(result.blockers).toHaveLength(0);
    expect(result.blocking).toHaveLength(0);
  });

  it('handles a mix of blockers and blocking for the same item', () => {
    const links = [
      // 100 depends on 300 → blocker
      link({
        id: 10,
        source_id: 100,
        target_id: 300,
        target_title: 'Dep A',
        target_item_number: 30,
      }),
      // 250 depends on 100 → 100 blocks 250
      link({
        id: 11,
        source_id: 250,
        target_id: 100,
        source_title: 'Blocked B',
        source_item_number: 25,
      }),
      // unrelated pair
      link({ id: 12, source_id: 400, target_id: 500 }),
    ];
    const { blockers, blocking } = splitDependencies(links, 100);
    expect(blockers.map((b) => b.title)).toEqual(['Dep A']);
    expect(blocking.map((b) => b.title)).toEqual(['Blocked B']);
  });

  it('returns empty lists for null/empty input', () => {
    expect(splitDependencies(null, 100)).toEqual({ blockers: [], blocking: [] });
    expect(splitDependencies([], 100)).toEqual({ blockers: [], blocking: [] });
    expect(splitDependencies(undefined, 100)).toEqual({ blockers: [], blocking: [] });
  });

  it('matches itemId as string or number', () => {
    const { blockers } = splitDependencies([link()], '100');
    expect(blockers).toHaveLength(1);
  });
});

describe('dependencyKey', () => {
  it('builds the workspace-scoped key from number', () => {
    expect(dependencyKey({ keyPrefix: 'WI', itemNumber: 74 })).toBe('WI-74');
  });

  it('falls back to the raw id when no item number', () => {
    expect(dependencyKey({ keyPrefix: 'WI', id: 149 })).toBe('WI-149');
  });

  it('uses a WORK fallback when no prefix', () => {
    expect(dependencyKey({ itemNumber: 5 })).toBe('WORK-5');
  });

  it('returns empty for falsy input', () => {
    expect(dependencyKey(null)).toBe('');
    expect(dependencyKey(undefined)).toBe('');
  });
});
