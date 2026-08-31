import { describe, expect, it, vi } from 'vitest';
import {
  buildPermissionAssignmentMap,
  loadPermissionManagerData,
} from './permissionManagerData.js';

describe('permission manager bulk request graph', () => {
  it('loads catalogs and both assignment sets once without per-user requests', async () => {
    const apiClient = {
      permissions: {
        getAll: vi.fn().mockResolvedValue([{ id: 1 }]),
        getAllUserGlobalPermissions: vi.fn().mockResolvedValue([
          { user_id: 10, permission_id: 1 },
          { user_id: 10, permission_id: 2 },
          { user_id: 11, permission_id: 2 },
        ]),
        getAllGroupPermissions: vi.fn().mockResolvedValue([{ group_id: 20, permission_id: 2 }]),
        getUserPermissions: vi.fn(),
      },
      getUsers: vi.fn().mockResolvedValue([{ id: 10 }, { id: 11 }]),
      groups: { getAll: vi.fn().mockResolvedValue([{ id: 20 }]) },
    };

    const loading = loadPermissionManagerData(apiClient);

    expect(apiClient.permissions.getAll).toHaveBeenCalledOnce();
    expect(apiClient.getUsers).toHaveBeenCalledOnce();
    expect(apiClient.groups.getAll).toHaveBeenCalledOnce();
    expect(apiClient.permissions.getAllUserGlobalPermissions).toHaveBeenCalledOnce();
    expect(apiClient.permissions.getAllGroupPermissions).toHaveBeenCalledOnce();
    expect(apiClient.permissions.getUserPermissions).not.toHaveBeenCalled();

    const data = await loading;
    expect([...data.userPermissions.get(10)]).toEqual([1, 2]);
    expect([...data.userPermissions.get(11)]).toEqual([2]);
    expect([...data.groupPermissions.get(20)]).toEqual([2]);
  });

  it('reuses a seeded permission catalog from the shared shell store', async () => {
    const apiClient = {
      permissions: {
        getAll: vi.fn(),
        getAllUserGlobalPermissions: vi.fn().mockResolvedValue([]),
        getAllGroupPermissions: vi.fn().mockResolvedValue([]),
      },
      getUsers: vi.fn().mockResolvedValue([]),
      groups: { getAll: vi.fn().mockResolvedValue([]) },
    };
    const seeded = [{ id: 7, permission_key: 'workspace.view' }];

    const data = await loadPermissionManagerData(apiClient, { permissions: seeded });

    expect(data.permissions).toEqual(seeded);
    expect(apiClient.permissions.getAll).not.toHaveBeenCalled();
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
