import { beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('../api/permissions.js', () => ({
  permissions: {
        getUserPermissions: vi.fn(),
      },
}))

vi.mock('../api/auth.js', () => ({
  auth: {
        getCurrentUser: vi.fn(),
      },
}));

import { permissions } from '../api/permissions.js';
import { authStore } from './auth.svelte.js';
import { clearPermissionProfiles } from './permissionProfile.js';
import { workspacePermissions } from './workspacePermissions.svelte.js';

import { auth } from '../api/auth.js';


// Builds the compact WI-1444 profile: workspace_permissions maps a stringified
// workspace ID to its permission keys. Entries for the same workspace merge,
// matching how the server groups grants.
function profileFor(workspaces) {
  const merged = {};
  for (const [workspaceId, keys] of workspaces) {
    merged[workspaceId] = [
      ...(merged[workspaceId] ?? []),
      ...(Array.isArray(keys) ? keys : [keys]),
    ];
  }
  return { global_permissions: [], workspace_permissions: merged };
}

beforeEach(() => {
  clearPermissionProfiles();
  workspacePermissions.clear();
  vi.clearAllMocks();
  authStore.setAuthData({ id: 7, is_system_admin: false }, { id: 'session-1' });
});

describe('workspacePermissions reload', () => {
  test('refreshes a stale profile so a newly-created workspace grants admin', async () => {
    // The profile cached at bootstrap predates workspace creation, so the new
    // workspace is absent from it.
    permissions.getUserPermissions
      .mockResolvedValueOnce(profileFor([]))
      .mockResolvedValueOnce(profileFor([[3, 'workspace.admin']]));

    await workspacePermissions.loadPermissions(7);
    expect(workspacePermissions.canAdminWorkspace(3)).toBe(false);

    await workspacePermissions.reload();

    expect(workspacePermissions.canAdminWorkspace(3)).toBe(true);
    expect(permissions.getUserPermissions).toHaveBeenCalledTimes(2);
    expect(permissions.getUserPermissions).toHaveBeenLastCalledWith(7);
  });

  test('reload uses the signed-in user without arguments', async () => {
    permissions.getUserPermissions.mockResolvedValue(profileFor([]));

    await workspacePermissions.reload();

    expect(permissions.getUserPermissions).toHaveBeenCalledWith(7);
  });
});

describe('page permission helpers', () => {
  test('canCreatePages accepts page.create, page.admin, or workspace.admin', async () => {
    permissions.getUserPermissions
      .mockResolvedValueOnce(profileFor([[3, 'page.create']]))
      .mockResolvedValueOnce(profileFor([[3, 'page.admin']]))
      .mockResolvedValueOnce(profileFor([[3, 'workspace.admin']]));

    await workspacePermissions.loadPermissions(7);
    expect(workspacePermissions.canCreatePages(3)).toBe(true);
    expect(workspacePermissions.canDeletePages(3)).toBe(false);

    await workspacePermissions.reload();
    expect(workspacePermissions.canCreatePages(3)).toBe(true);

    await workspacePermissions.reload();
    expect(workspacePermissions.canCreatePages(3)).toBe(true);
  });

  test('canCreatePages denies page.view-only callers; page.delete alone stays denied', async () => {
    permissions.getUserPermissions
      .mockResolvedValueOnce(profileFor([[3, 'page.view']]))
      .mockResolvedValueOnce(profileFor([[3, 'page.view'], [3, 'page.delete']]));

    await workspacePermissions.loadPermissions(7);
    expect(workspacePermissions.canCreatePages(3)).toBe(false);
    expect(workspacePermissions.canDeletePages(3)).toBe(false);

    await workspacePermissions.reload();
    expect(workspacePermissions.canCreatePages(3)).toBe(false);
    expect(workspacePermissions.canDeletePages(3)).toBe(true);
  });

  test('page permission grants are workspace-scoped', async () => {
    permissions.getUserPermissions.mockResolvedValue(
      profileFor([[3, 'page.create'], [3, 'page.delete']]),
    );
    await workspacePermissions.loadPermissions(7);

    expect(workspacePermissions.canCreatePages(9)).toBe(false);
    expect(workspacePermissions.canDeletePages(9)).toBe(false);
  });
});
