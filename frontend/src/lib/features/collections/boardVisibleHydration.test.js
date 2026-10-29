import { describe, expect, test } from 'vitest';
import {
  loadedVisibleItemIDs,
  visibleAssigneeIDs,
} from './boardHydration.js';

function itemsByID(items) {
  return new Map(items.map((item) => [item.id, item]));
}

describe('board visible hydration', () => {
  test('keeps offscreen and collapsed cards out of optional reads', () => {
    const items = itemsByID([
      { id: 1, assignee_id: 11 },
      { id: 2, assignee_id: 22 },
      { id: 3, assignee_id: 33 },
    ]);

    // Only card 2 is intersecting; cards 1 and 3 are offscreen or collapsed.
    expect(loadedVisibleItemIDs(new Set([2]), items)).toEqual([2]);
    expect(visibleAssigneeIDs(new Set([2]), items)).toEqual([22]);
  });

  test('ignores visible ids that are no longer loaded', () => {
    const items = itemsByID([{ id: 4, assignee_id: 44 }]);

    expect(loadedVisibleItemIDs(new Set([4, 99]), items)).toEqual([4]);
    expect(visibleAssigneeIDs(new Set([4, 99]), items)).toEqual([44]);
  });

  test('deduplicates assignees shared by visible cards', () => {
    const items = itemsByID([
      { id: 5, assignee_id: 7 },
      { id: 6, assignee_id: 7 },
      { id: 7 },
    ]);

    expect(visibleAssigneeIDs(new Set([5, 6, 7]), items)).toEqual([7]);
  });
});
