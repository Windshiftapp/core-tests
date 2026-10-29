import { describe, expect, it } from 'vitest';

import { findOrphansByMissingParent, indexCollectionHierarchy, mergeAncestorContext } from './collectionHierarchy.js';

describe('indexCollectionHierarchy', () => {
  it('indexes roots and children in one pass while preserving order', () => {
    const orphan = { id: 4, parent_id: 99 };
    const root = { id: 1, parent_id: null };
    const firstChild = { id: 2, parent_id: 1 };
    const secondChild = { id: 3, parent_id: 1 };

    const index = indexCollectionHierarchy([orphan, root, firstChild, secondChild]);

    expect(index.roots).toEqual([orphan, root]);
    expect(index.childrenByParent.get(1)).toEqual([firstChild, secondChild]);
    expect(index.childrenByParent.has(2)).toBe(false);
  });
});

describe('findOrphansByMissingParent', () => {
  it('groups loaded items by the parent id missing from the set', () => {
    const items = [
      { id: 1, parent_id: null },
      { id: 2, parent_id: 1 },
      { id: 3, parent_id: 13 },
      { id: 4, parent_id: 13 },
      { id: 5, parent_id: 2 },
    ];

    const orphans = findOrphansByMissingParent(items);

    expect(orphans.size).toBe(1);
    expect(orphans.get(13)).toEqual([
      { id: 3, parent_id: 13 },
      { id: 4, parent_id: 13 },
    ]);
  });

  it('returns an empty map when every parent is loaded', () => {
    const items = [
      { id: 1, parent_id: null },
      { id: 2, parent_id: 1 },
    ];

    expect(findOrphansByMissingParent(items).size).toBe(0);
  });
});

describe('mergeAncestorContext', () => {
  it('appends ancestor chains for missing parents without duplicates', () => {
    const items = [
      { id: 3, parent_id: 13 },
      { id: 4, parent_id: 27 },
    ];
    const ancestorsByParent = {
      13: [
        { id: 1, parent_id: null },
        { id: 13, parent_id: 1 },
      ],
      27: [
        { id: 1, parent_id: null },
        { id: 27, parent_id: 1 },
      ],
    };

    const merged = mergeAncestorContext(items, ancestorsByParent);

    expect(merged.map((item) => item.id)).toEqual([3, 4, 1, 13, 27]);
  });

  it('ignores chains whose parent has since been loaded by the query', () => {
    const items = [
      { id: 13, parent_id: null },
      { id: 3, parent_id: 13 },
    ];
    const ancestorsByParent = {
      13: [
        { id: 1, parent_id: null },
        { id: 13, parent_id: 1 },
      ],
    };

    const merged = mergeAncestorContext(items, ancestorsByParent);

    expect(merged.map((item) => item.id)).toEqual([13, 3]);
  });

  it('returns the original items when no ancestor context exists', () => {
    const items = [{ id: 3, parent_id: 13 }];

    expect(mergeAncestorContext(items, null)).toBe(items);
    expect(mergeAncestorContext(items, {})).toEqual(items);
  });
});
