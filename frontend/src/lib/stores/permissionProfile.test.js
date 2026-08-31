import { beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('../api.js', () => ({
  api: {
    permissions: {
      getUserPermissions: vi.fn(),
    },
  },
}));

import { api } from '../api.js';
import {
  beginPermissionProfileGeneration,
  clearPermissionProfiles,
  loadPermissionProfile,
} from './permissionProfile.js';

beforeEach(() => {
  clearPermissionProfiles();
  vi.clearAllMocks();
});

describe('permission profile single-flight cache', () => {
  test('shares one request between concurrent consumers in a generation', async () => {
    const profile = { global_permissions: [], workspace_permissions: [] };
    api.permissions.getUserPermissions.mockResolvedValue(profile);

    const [globalConsumer, workspaceConsumer] = await Promise.all([
      loadPermissionProfile(7),
      loadPermissionProfile(7),
    ]);

    expect(globalConsumer).toBe(profile);
    expect(workspaceConsumer).toBe(profile);
    expect(api.permissions.getUserPermissions).toHaveBeenCalledTimes(1);
  });

  test('fetches a fresh profile for a new authenticated-shell generation', async () => {
    api.permissions.getUserPermissions.mockResolvedValue({ global_permissions: [] });
    await loadPermissionProfile(7);
    beginPermissionProfileGeneration();
    await loadPermissionProfile(7);

    expect(api.permissions.getUserPermissions).toHaveBeenCalledTimes(2);
  });

  test('does not cache a failed request', async () => {
    api.permissions.getUserPermissions
      .mockRejectedValueOnce(new Error('temporary'))
      .mockResolvedValueOnce({ workspace_permissions: [] });

    await expect(loadPermissionProfile(7)).rejects.toThrow('temporary');
    await expect(loadPermissionProfile(7)).resolves.toEqual({ workspace_permissions: [] });
    expect(api.permissions.getUserPermissions).toHaveBeenCalledTimes(2);
  });
});
