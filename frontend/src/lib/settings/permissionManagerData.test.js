import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api/permissions.js', () => ({
  permissions: {
    getAll: vi.fn(),
    getAllUserGlobalPermissions: vi.fn(),
    getAllGroupPermissions: vi.fn(),
    getUserPermissions: vi.fn(),
  },
  groups: { getAll: vi.fn() },
}));
vi.mock('../api/users.js', () => ({
  getUsers: vi.fn(),
}));

import { groups, permissions } from '../api/permissions.js';
import { getUsers } from '../api/users.js';
import { buildPermissionAssignmentMap, loadPermissionManagerData, selectGlobalPermissions } from './permissionManagerData.js';

describe('permission manager bulk request graph', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('loads catalogs and both assignment sets once without per-user requests', async () => {
    permissions.getAll.mockResolvedValue([{ id: 1 }]);
    permissions.getAllUserGlobalPermissions.mockResolvedValue([
      { user_id: 10, permission_id: 1 },
      { user_id: 10, permission_id: 2 },
      { user_id: 11, permission_id: 2 },
    ]);
    permissions.getAllGroupPermissions.mockResolvedValue([{ group_id: 20, permission_id: 2 }]);
    getUsers.mockResolvedValue([{ id: 10 }, { id: 11 }]);
    groups.getAll.mockResolvedValue([{ id: 20 }]);

    const loading = loadPermissionManagerData();

    expect(permissions.getAll).toHaveBeenCalledOnce();
    expect(getUsers).toHaveBeenCalledOnce();
    expect(groups.getAll).toHaveBeenCalledOnce();
    expect(permissions.getAllUserGlobalPermissions).toHaveBeenCalledOnce();
    expect(permissions.getAllGroupPermissions).toHaveBeenCalledOnce();
    expect(permissions.getUserPermissions).not.toHaveBeenCalled();

    const data = await loading;
    expect([...data.userPermissions.get(10)]).toEqual([1, 2]);
    expect([...data.userPermissions.get(11)]).toEqual([2]);
    expect([...data.groupPermissions.get(20)]).toEqual([2]);
  });

  it('reuses a seeded permission catalog from the shared shell store', async () => {
    permissions.getAllUserGlobalPermissions.mockResolvedValue([]);
    permissions.getAllGroupPermissions.mockResolvedValue([]);
    getUsers.mockResolvedValue([]);
    groups.getAll.mockResolvedValue([]);
    const seeded = [{ id: 7, permission_key: 'workspace.view' }];

    const data = await loadPermissionManagerData({ permissions: seeded });

    expect(data.permissions).toEqual(seeded);
    expect(permissions.getAll).not.toHaveBeenCalled();
  });
});

describe('permission manager helpers', () => {
  it('exposes every global permission, including user.list, for assignment', () => {
    const selected = selectGlobalPermissions([
      { id: 1, permission_key: 'user.list', scope: 'global' },
      { id: 2, permission_key: 'system.admin', scope: 'global' },
      { id: 3, permission_key: 'action.manage', scope: 'workspace' },
    ]);

    expect(selected.map((permission) => permission.permission_key)).toEqual([
      'user.list',
      'system.admin',
    ]);
  });

  it('deduplicates assignments and ignores malformed rows', () => {
    const result = buildPermissionAssignmentMap(
      [
        { user_id: 7, permission_id: 3 },
        { user_id: 7, permission_id: 3 },
        { user_id: null, permission_id: 4 },
        null,
      ],
      'user_id'
    );

    expect([...result.get(7)]).toEqual([3]);
    expect(result.size).toBe(1);
  });
});
