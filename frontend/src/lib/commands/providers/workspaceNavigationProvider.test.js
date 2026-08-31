import { beforeEach, describe, expect, test, vi } from 'vitest';

const permissionState = vi.hoisted(() => ({ admin: false }));

vi.mock('../../stores', () => ({
  workspacePermissions: {
    canAdminWorkspace: vi.fn(() => permissionState.admin),
    canViewTests: vi.fn(() => false),
  },
}));

import { workspaceNavigationProvider } from './workspaceNavigationProvider.js';

function commandIds() {
  return workspaceNavigationProvider({
    workspaceId: 7,
    workspace: { name: 'Platform' },
    collectionId: null,
    route: { path: '/workspaces/7/board' },
    modules: { test_management_enabled: false },
  }).map((command) => command.id);
}

describe('workspaceNavigationProvider Agent Studio visibility', () => {
  beforeEach(() => {
    permissionState.admin = false;
  });

  test('hides Agents from non-admin workspace navigation commands', () => {
    expect(commandIds()).not.toContain('workspace-agents-view');
  });

  test('shows Agents to workspace admins', () => {
    permissionState.admin = true;

    expect(commandIds()).toContain('workspace-agents-view');
  });
});
