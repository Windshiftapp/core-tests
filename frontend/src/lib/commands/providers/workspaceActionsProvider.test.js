import { beforeEach, describe, expect, test, vi } from 'vitest';

const permissionState = vi.hoisted(() => ({ admin: false }));

vi.mock('../../stores/workspacePermissions.svelte.js', () => ({
  workspacePermissions: {
      canAdminWorkspace: vi.fn(() => permissionState.admin),
      canViewTests: vi.fn(() => false),
    },
}));

import { workspaceActionsProvider } from './workspaceActionsProvider.js';

function commandIds() {
  return workspaceActionsProvider({
    t: (key) => key,
    workspaceId: 7,
    workspace: { name: 'Platform' },
    collectionId: null,
    modules: { test_management_enabled: false },
  }).map((command) => command.id);
}

// Board configuration saves are gated server-side on workspace.admin for the
// workspace scope (WI-1361); the palette must not offer the entry point to
// users who cannot save.
describe('workspaceActionsProvider configure-board visibility', () => {
  beforeEach(() => {
    permissionState.admin = false;
  });

  test('hides workspace-configure-board from non-admins', () => {
    expect(commandIds()).not.toContain('workspace-configure-board');
  });

  test('hides collection-configure-board from non-admins', () => {
    const ids = workspaceActionsProvider({
      t: (key) => key,
      workspaceId: 7,
      workspace: { name: 'Platform' },
      collectionId: '3',
      modules: { test_management_enabled: false },
    }).map((command) => command.id);

    expect(ids).not.toContain('collection-configure-board');
  });

  test('shows workspace-configure-board to workspace admins', () => {
    permissionState.admin = true;

    expect(commandIds()).toContain('workspace-configure-board');
  });

  test('shows collection-configure-board to workspace admins', () => {
    permissionState.admin = true;

    const ids = workspaceActionsProvider({
      t: (key) => key,
      workspaceId: 7,
      workspace: { name: 'Platform' },
      collectionId: '3',
      modules: { test_management_enabled: false },
    }).map((command) => command.id);

    expect(ids).toContain('collection-configure-board');
  });
});
