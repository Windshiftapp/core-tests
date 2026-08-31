import { describe, expect, test } from 'vitest';

import {
  canItemTypeBeChildOf,
  childItemTypesForParent,
  GENERIC_SUBTASK_HIERARCHY_LEVEL,
  isGenericSubtaskType,
  sortItemTypesByHierarchy,
} from './hierarchy.js';

const epic = { id: 1, name: 'Epic', hierarchy_level: 0, sort_order: 1 };
const story = { id: 2, name: 'Story', hierarchy_level: 1, sort_order: 2 };
const task = { id: 3, name: 'Task', hierarchy_level: 2, sort_order: 3 };
const genericSubtask = {
  id: 4,
  name: 'Sub-task',
  hierarchy_level: GENERIC_SUBTASK_HIERARCHY_LEVEL,
  sort_order: 4,
};

describe('generic subtask hierarchy', () => {
  test('recognizes level -1 as the generic subtask sentinel', () => {
    expect(isGenericSubtaskType(genericSubtask)).toBe(true);
    expect(isGenericSubtaskType(task)).toBe(false);
  });

  test.each([epic, story, task])(
    'allows a generic subtask below regular parent $name',
    (parentType) => {
      expect(canItemTypeBeChildOf(genericSubtask, parentType)).toBe(true);
    }
  );

  test('treats a generic subtask as terminal', () => {
    expect(canItemTypeBeChildOf(task, genericSubtask)).toBe(false);
    expect(canItemTypeBeChildOf(genericSubtask, genericSubtask)).toBe(false);
    expect(childItemTypesForParent([epic, story, task, genericSubtask], genericSubtask)).toEqual(
      []
    );
  });

  test('keeps the adjacent-level rule for regular item types', () => {
    expect(canItemTypeBeChildOf(story, epic)).toBe(true);
    expect(canItemTypeBeChildOf(task, epic)).toBe(false);
    expect(childItemTypesForParent([epic, story, task, genericSubtask], epic)).toEqual([
      story,
      genericSubtask,
    ]);
  });

  test('sorts the generic subtask after regular hierarchy levels', () => {
    expect(sortItemTypesByHierarchy([genericSubtask, task, epic, story])).toEqual([
      epic,
      story,
      task,
      genericSubtask,
    ]);
  });
});
