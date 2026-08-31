import { describe, expect, it } from 'vitest';
import { buildHierarchyDatePatches, projectHierarchyDates } from './roadmapHierarchyDates.js';

function item(id, parentId, startDate, endDate) {
  return {
    id,
    parent_id: parentId,
    start_date: startDate,
    end_date: endDate,
  };
}

describe('projectHierarchyDates', () => {
  it('rolls nested child ranges up without changing source items', () => {
    const items = [
      item(1, null, '2026-08-01', '2026-08-31'),
      item(2, 1, '2026-08-05', '2026-08-25'),
      item(3, 2, '2026-08-10', '2026-08-12'),
      item(4, 2, '2026-08-18', '2026-08-20'),
    ];

    const projected = projectHierarchyDates(items, 'rollup');

    expect(projected.get(2)).toMatchObject({
      startDate: '2026-08-10',
      endDate: '2026-08-20',
      adjusted: true,
      summary: true,
    });
    expect(projected.get(1)).toMatchObject({
      startDate: '2026-08-10',
      endDate: '2026-08-20',
      adjusted: true,
      summary: true,
    });
    expect(items[0]).toEqual(item(1, null, '2026-08-01', '2026-08-31'));
  });

  it('rolls dates down by clamping only boundaries outside the parent', () => {
    const projected = projectHierarchyDates(
      [
        item(1, null, '2026-08-10', '2026-08-20'),
        item(2, 1, '2026-08-12', '2026-08-18'),
        item(3, 1, '2026-08-05', '2026-08-15'),
        item(4, 1, '2026-08-16', '2026-08-25'),
        item(5, 1, '2026-08-25', '2026-08-28'),
      ],
      'rolldown'
    );

    expect(projected.get(2)).toMatchObject({
      startDate: '2026-08-12',
      endDate: '2026-08-18',
      adjusted: false,
    });
    expect(projected.get(3)).toMatchObject({
      startDate: '2026-08-10',
      endDate: '2026-08-15',
      adjusted: true,
    });
    expect(projected.get(4)).toMatchObject({
      startDate: '2026-08-16',
      endDate: '2026-08-20',
      adjusted: true,
    });
    expect(projected.get(5)).toMatchObject({
      startDate: '2026-08-20',
      endDate: '2026-08-20',
      adjusted: true,
    });
  });
});

describe('buildHierarchyDatePatches', () => {
  it('persists an edited leaf and the recalculated ancestor range in rollup mode', () => {
    const patches = buildHierarchyDatePatches({
      items: [
        item(1, null, '2026-08-01', '2026-08-31'),
        item(2, 1, '2026-08-10', '2026-08-12'),
        item(3, 1, '2026-08-18', '2026-08-20'),
      ],
      editedItemId: 2,
      fields: { start_date: '2026-08-08', end_date: '2026-08-12' },
      mode: 'rollup',
    });

    expect(patches).toEqual([
      { item_id: 1, set: { start_date: '2026-08-08', end_date: '2026-08-20' } },
      { item_id: 2, set: { start_date: '2026-08-08', end_date: '2026-08-12' } },
    ]);
  });

  it('persists only descendants that exceed an edited parent in rolldown mode', () => {
    const patches = buildHierarchyDatePatches({
      items: [
        item(1, null, '2026-08-01', '2026-08-31'),
        item(2, 1, '2026-08-12', '2026-08-18'),
        item(3, 1, '2026-08-16', '2026-08-25'),
        item(4, 3, '2026-08-24', '2026-08-27'),
      ],
      editedItemId: 1,
      fields: { start_date: '2026-08-10', end_date: '2026-08-20' },
      mode: 'rolldown',
    });

    expect(patches).toEqual([
      { item_id: 1, set: { start_date: '2026-08-10', end_date: '2026-08-20' } },
      { item_id: 3, set: { end_date: '2026-08-20' } },
      { item_id: 4, set: { start_date: '2026-08-20', end_date: '2026-08-20' } },
    ]);
  });
});
