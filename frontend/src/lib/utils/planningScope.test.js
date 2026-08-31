import { describe, expect, test } from 'vitest';

import { canChangePlanningScope, preservePlanningScope } from './planningScope.js';

describe('planning scope edit policy', () => {
  test.each(['iteration', 'milestone'])(
    'allows %s scope selection during creation for global managers',
    () => {
      expect(canChangePlanningScope(true, null)).toBe(true);
    }
  );

  test.each([
    ['iteration', false, 3],
    ['iteration', true, null],
    ['milestone', false, 3],
    ['milestone', true, null],
  ])('keeps the %s toggle hidden while editing is_global=%s', (_, isGlobal, workspaceId) => {
    expect(
      canChangePlanningScope(true, {
        id: 42,
        is_global: isGlobal,
        workspace_id: workspaceId,
      })
    ).toBe(false);
  });
});

describe('planning scope update payloads', () => {
  test.each(['iteration', 'milestone'])(
    'preserves an existing global %s when updates request local scope',
    () => {
      expect(
        preservePlanningScope(
          { name: 'Updated', is_global: false, workspace_id: 3 },
          { id: 42, is_global: true, workspace_id: null }
        )
      ).toEqual({
        name: 'Updated',
        is_global: true,
        workspace_id: null,
      });
    }
  );

  test.each(['iteration', 'milestone'])(
    'preserves an existing local %s when updates request global scope',
    () => {
      expect(
        preservePlanningScope(
          { name: 'Updated', is_global: true, workspace_id: null },
          { id: 42, is_global: false, workspace_id: 3 }
        )
      ).toEqual({
        name: 'Updated',
        is_global: false,
        workspace_id: 3,
      });
    }
  );
});
