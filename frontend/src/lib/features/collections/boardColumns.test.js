import { describe, expect, it } from 'vitest';

import {
  boardStatusIdForItem,
  PERSONAL_TASK_DONE_STATUS_ID,
  PERSONAL_TASK_OPEN_STATUS_ID,
  statusIdForBoardColumnMove,
} from './boardColumns.js';

const columns = [
  { id: 10, name: 'Ready', status_ids: [101] },
  { id: 20, name: 'Doing', status_ids: [202] },
  { id: 30, name: 'Shipped', status_ids: [303] },
];
const personalWorkspaceIds = new Set([9]);

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
