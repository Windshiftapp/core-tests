import { beforeEach, describe, expect, test, vi } from 'vitest';

const permissionState = vi.hoisted(() => ({ admin: false, permissions: {} }));

vi.mock('../../stores/workspacePermissions.svelte.js', () => ({
  workspacePermissions: {
      canAdminWorkspace: vi.fn(() => permissionState.admin),
      canViewTests: vi.fn(() => false),
      hasPermission: vi.fn((_workspaceId, permission) => {
        return permissionState.permissions[permission] === true;
      }),
    },
}));

import { workspaceNavigationProvider } from './workspaceNavigationProvider.js';

function commands() {
  return workspaceNavigationProvider({
    workspaceId: 7,
    workspace: { name: 'Platform' },
    collectionId: null,
    route: { path: '/workspaces/7/board' },
    modules: { test_management_enabled: false },
  });
}

function commandIds() {
  return commands().map((command) => command.id);
}

describe('workspaceNavigationProvider Agent Studio visibility', () => {
  beforeEach(() => {
    permissionState.admin = false;
    permissionState.permissions = {};
  });

  test('hides Agents from non-admin workspace navigation commands', () => {
    expect(commandIds()).not.toContain('workspace-agents-view');
  });

  test('shows Agents to workspace admins', () => {
    permissionState.admin = true;

    expect(commandIds()).toContain('workspace-agents-view');
  });
});

describe('workspaceNavigationProvider workspace settings', () => {
  beforeEach(() => {
    permissionState.admin = false;
    permissionState.permissions = {};
  });

  test('hides workspace settings from non-admins', () => {
    expect(commandIds()).not.toContain('workspace-settings-general');
  });

  test('shows ungated settings modules to workspace admins', () => {
    permissionState.admin = true;

    const ids = commandIds();

    expect(ids).toContain('workspace-settings-general');
    expect(ids).toContain('workspace-settings-members');
  });

  test('links a settings command to its module route', () => {
    permissionState.admin = true;

    const general = commands().find((command) => command.id === 'workspace-settings-general');

    expect(general?.url).toBe('/workspaces/7/settings/general');
  });

  test('hides a permissioned settings module the admin lacks', () => {
    permissionState.admin = true;

    expect(commandIds()).not.toContain('workspace-settings-action-credentials');
  });

  test('shows a permissioned settings module once granted', () => {
    permissionState.admin = true;
    permissionState.permissions = { 'action.credential.manage': true };

    expect(commandIds()).toContain('workspace-settings-action-credentials');
  });

  test('omits settings commands in a collection context', () => {
    permissionState.admin = true;

    const ids = workspaceNavigationProvider({
      workspaceId: 7,
      workspace: { name: 'Platform' },
      collectionId: 3,
      route: { path: '/workspaces/7/collections/3/board' },
      modules: { test_management_enabled: false },
    }).map((command) => command.id);

    expect(ids).not.toContain('workspace-settings-general');
  });
});
