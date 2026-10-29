import { describe, expect, it } from 'vitest';

import { boardStatusIdForItem, buildDisplayColumns, PERSONAL_TASK_DONE_STATUS_ID, PERSONAL_TASK_OPEN_STATUS_ID, sortStatusesForBoard, statusIdForBoardColumnMove } from './boardColumns.js';

const columns = [
  { id: 10, name: 'Ready', status_ids: [101] },
  { id: 20, name: 'Doing', status_ids: [202] },
  { id: 30, name: 'Shipped', status_ids: [303] },
];
const personalWorkspaceIds = new Set([9]);

describe('default board column ordering', () => {
  const status = (id, name, builtinKey, isCompleted) => ({
    id,
    name,
    category_builtin_key: builtinKey,
    is_completed: isCompleted,
  });

  it('places completed categories last and custom categories before them', () => {
    const ordered = sortStatusesForBoard([
      status(4, 'Done', 'done', true),
      status(3, 'UT Testing', null, false),
      status(1, 'Open', 'to_do', false),
      status(2, 'In Progress', 'in_progress', false),
    ]);

    expect(ordered.map((s) => s.name)).toEqual(['Open', 'In Progress', 'UT Testing', 'Done']);
  });

  it('keeps to_do before in_progress and sorts custom categories alphabetically', () => {
    const ordered = sortStatusesForBoard([
      status(5, 'UA Testing', null, false),
      status(2, 'In Progress', 'in_progress', false),
      status(4, 'UT Testing', null, false),
      status(1, 'Open', 'to_do', false),
    ]);

    expect(ordered.map((s) => s.name)).toEqual([
      'Open',
      'In Progress',
      'UA Testing',
      'UT Testing',
    ]);
  });

  it('reads category metadata from the nested category shape', () => {
    const ordered = sortStatusesForBoard([
      { id: 2, name: 'Done', category: { builtin_key: 'done', is_completed: true } },
      { id: 1, name: 'Open', category: { builtin_key: 'to_do', is_completed: false } },
    ]);

    expect(ordered.map((s) => s.name)).toEqual(['Open', 'Done']);
  });

  it('orders completed categories after all open work', () => {
    const ordered = sortStatusesForBoard([
      status(6, 'Archived', 'archived', true),
      status(5, 'Done', 'done', true),
      status(1, 'Open', 'to_do', false),
    ]);

    expect(ordered.map((s) => s.name)).toEqual(['Open', 'Archived', 'Done']);
  });
});

describe('personal task board status mapping', () => {
  it('places Open and Done in the board endpoint columns', () => {
    expect(
      boardStatusIdForItem(
        { workspace_id: 9, status_id: PERSONAL_TASK_OPEN_STATUS_ID },
        columns,
        personalWorkspaceIds
      )
    ).toBe(101);
    expect(
      boardStatusIdForItem(
        { workspace_id: 9, status_id: PERSONAL_TASK_DONE_STATUS_ID },
        columns,
        personalWorkspaceIds
      )
    ).toBe(303);
  });

  it('keeps regular work items on their actual status', () => {
    expect(
      boardStatusIdForItem({ workspace_id: 8, status_id: 202 }, columns, personalWorkspaceIds)
    ).toBe(202);
  });

  it('translates endpoint-column moves and rejects intermediate columns', () => {
    const task = { workspace_id: 9, status_id: PERSONAL_TASK_OPEN_STATUS_ID };

    expect(statusIdForBoardColumnMove(task, columns[0], columns, personalWorkspaceIds)).toBe(
      PERSONAL_TASK_OPEN_STATUS_ID
    );
    expect(statusIdForBoardColumnMove(task, columns[1], columns, personalWorkspaceIds)).toBeNull();
    expect(statusIdForBoardColumnMove(task, columns[2], columns, personalWorkspaceIds)).toBe(
      PERSONAL_TASK_DONE_STATUS_ID
    );
  });
});
