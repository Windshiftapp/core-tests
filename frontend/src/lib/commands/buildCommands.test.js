import { describe, expect, it } from 'vitest';
import { BUCKET } from './buckets.js';
import { buildCommands } from './buildCommands.js';
import { adminProvider } from './providers/adminProvider.js';
import { globalNavigationProvider } from './providers/globalNavigationProvider.js';
import { timeProvider } from './providers/timeProvider.js';
import { workspacesProvider } from './providers/workspacesProvider.js';

describe('buildCommands', () => {
  const baseCtx = {
    route: { view: 'home', params: {}, path: '/' },
    permissions: {},
    isSystemAdmin: false,
    modules: {},
    workspaces: [],
    workspaceId: null,
    workspace: null,
    collectionId: null,
    itemId: null,
    workItems: [],
    activeTimer: null,
    t: (k) => k,
    query: '',
  };

  it('calls each provider in order and stamps _seq', () => {
    const p1 = () => [{ id: 'a', label: 'A', bucket: BUCKET.GLOBAL_NAVIGATION }];
    const p2 = () => [
      { id: 'b', label: 'B', bucket: BUCKET.GLOBAL_NAVIGATION },
      { id: 'c', label: 'C', bucket: BUCKET.GLOBAL_NAVIGATION },
    ];
    const result = buildCommands(baseCtx, [p1, p2]);
    expect(result.map((c) => c.id)).toEqual(['a', 'b', 'c']);
    expect(result.map((c) => c._seq)).toEqual([0, 1, 2]);
  });

  it('filters out commands whose isAvailable returns false', () => {
    const p = () => [
      { id: 'visible', label: 'V', bucket: BUCKET.GLOBAL_NAVIGATION },
      {
        id: 'hidden',
        label: 'H',
        bucket: BUCKET.GLOBAL_NAVIGATION,
        isAvailable: () => false,
      },
    ];
    const result = buildCommands(baseCtx, [p]);
    expect(result.map((c) => c.id)).toEqual(['visible']);
  });

  it('survives a provider that throws', () => {
    const bad = () => {
      throw new Error('boom');
    };
    const good = () => [{ id: 'ok', label: 'OK', bucket: BUCKET.SYSTEM }];
    const result = buildCommands(baseCtx, [bad, good]);
    expect(result.map((c) => c.id)).toEqual(['ok']);
  });

  it('skips null commands silently', () => {
    const p = () => [null, { id: 'x', label: 'X', bucket: BUCKET.SYSTEM }, undefined];
    const result = buildCommands(baseCtx, [p]);
    expect(result.map((c) => c.id)).toEqual(['x']);
  });
});

describe('integration: live providers', () => {
  it('admin provider is empty for non-admin users', () => {
    const ctx = {
      t: (k) => k,
      permissions: { canAccessAdmin: false },
    };
    expect(adminProvider(ctx)).toEqual([]);
  });

  it('admin provider emits tabs when canAccessAdmin', () => {
    const ctx = {
      t: (k) => k,
      permissions: { canAccessAdmin: true },
    };
    const cmds = adminProvider(ctx);
    expect(cmds.length).toBeGreaterThan(0);
    expect(cmds.every((c) => c.bucket === BUCKET.ADMIN)).toBe(true);
    expect(cmds.every((c) => c.url?.startsWith('/admin/'))).toBe(true);
  });

  it('global navigation excludes /dashboard', () => {
    const ctx = { t: (k) => k, permissions: {} };
    const cmds = globalNavigationProvider(ctx);
    expect(cmds.every((c) => c.url !== '/dashboard')).toBe(true);
  });

  it('global navigation hides items gated on missing permissions', () => {
    const noPerms = globalNavigationProvider({ t: (k) => k, permissions: {} });
    const withPerms = globalNavigationProvider({
      t: (k) => k,
      permissions: {
        canAccessLogbook: true,
        canAccessAssets: true,
        canAccessPortalHub: true,
        canAccessCustomers: true,
      },
    });
    expect(withPerms.length).toBeGreaterThan(noPerms.length);
  });

  it('time provider points worklogs at /time/worklogs (not /reports)', () => {
    const cmds = timeProvider({ t: (k) => k, activeTimer: null });
    const worklogs = cmds.find((c) => c.id === 'time-worklogs');
    expect(worklogs?.url).toBe('/time/worklogs');
    expect(cmds.some((c) => c.url === '/reports')).toBe(false);
    expect(cmds.some((c) => c.url === '/projects')).toBe(false);
  });

  it('time provider shows stop-timer when a timer is active, start-timer otherwise', () => {
    const noTimer = timeProvider({ t: (k) => k, activeTimer: null });
    const withTimer = timeProvider({ t: (k) => k, activeTimer: { id: 1 } });
    expect(noTimer.find((c) => c.id === 'start-timer')).toBeDefined();
    expect(noTimer.find((c) => c.id === 'stop-timer')).toBeUndefined();
    expect(withTimer.find((c) => c.id === 'start-timer')).toBeUndefined();
    expect(withTimer.find((c) => c.id === 'stop-timer')).toBeDefined();
  });

  it('workspaces provider hides inactive workspaces but keeps personal', () => {
    const cmds = workspacesProvider({
      t: (k) => k,
      workspaces: [
        { id: 1, name: 'Personal', is_personal: true },
        { id: 2, name: 'Active', active: true },
        { id: 3, name: 'Inactive', active: false },
      ],
    });
    const labels = cmds.map((c) => c.id);
    expect(labels).toContain('goto-workspace-1');
    expect(labels).toContain('goto-workspace-2');
    expect(labels).not.toContain('goto-workspace-3');
  });
});
