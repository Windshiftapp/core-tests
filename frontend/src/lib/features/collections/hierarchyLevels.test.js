import { describe, expect, it } from 'vitest';
import { buildHierarchyLevels, defaultHierarchyLevel } from './hierarchyLevels.js';

describe('buildHierarchyLevels', () => {
  it('merges item types and counts, skipping sub-tasks', () => {
    const levels = buildHierarchyLevels(
      [
        { id: 1, name: 'Task', hierarchy_level: 3 },
        { id: 2, name: 'Bug', hierarchy_level: 3 },
        { id: 3, name: 'Sub-task', hierarchy_level: -1 },
      ],
      [
        { level: 3, name: 'Task', total: 4 },
        { level: 4, name: '', total: 2 },
      ]
    );

    expect(levels.map((entry) => entry.level)).toEqual([3, 4]);
    expect(levels[0]).toMatchObject({ level: 3, total: 4, label: 'Task, Bug' });
    expect(levels[1]).toMatchObject({ level: 4, total: 2, label: 'Level 4' });
  });

  it('includes a level known only from counts', () => {
    const levels = buildHierarchyLevels([], [{ level: 0, name: 'Initiative', total: 0 }]);

    expect(levels).toEqual([
      { level: 0, types: [], name: 'Initiative', total: 0, label: 'Initiative' },
    ]);
  });
});

describe('defaultHierarchyLevel', () => {
  it('picks the shallowest level with matches', () => {
    expect(
      defaultHierarchyLevel([
        { level: 0, total: 0 },
        { level: 1, total: 0 },
        { level: 2, total: 3 },
      ])
    ).toBe(2);
  });

  it('falls back to the shallowest known level when nothing matches', () => {
    expect(
      defaultHierarchyLevel([
        { level: 1, total: 0 },
        { level: 2, total: 0 },
      ])
    ).toBe(1);
  });

  it('returns null when no level is known', () => {
    expect(defaultHierarchyLevel([])).toBeNull();
  });
});
