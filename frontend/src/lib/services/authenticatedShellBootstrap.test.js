import { beforeEach, describe, expect, test, vi } from 'vitest';
import { AUTHENTICATED_SHELL_BOOTSTRAP_BUDGET_MS, runAuthenticatedShellBootstrap } from './authenticatedShellBootstrap.js';

vi.mock('../stores/workspaces.svelte.js', () => ({
  workspacesStore: {
    load: vi.fn(() => Promise.resolve([])),
    loadPersonalWorkspace: vi.fn(() => Promise.resolve(null)),
  },
}));

vi.mock('../stores/permissions.svelte.js', () => ({
  permissionStore: {
    loadAllPermissions: vi.fn(() => Promise.resolve()),
    loadUserPermissions: vi.fn(() => Promise.resolve()),
  },
}));

vi.mock('../stores/workspacePermissions.svelte.js', () => ({
  workspacePermissions: { loadPermissions: vi.fn(() => Promise.resolve()) },
}));

vi.mock('../stores/sso.svelte.js', () => ({
  ssoStore: {
    getVerificationStatus: vi.fn(() =>
      Promise.resolve({ configured: false, email_verified: false })
    ),
  },
}));

vi.mock('../services/authenticatedShellUI.js', () => ({
  loadAuthenticatedShellUI: vi.fn(() => Promise.resolve()),
}));

import { loadAuthenticatedShellUI } from '../services/authenticatedShellUI.js';
import { buildAuthenticatedShellTasks } from '../pages/authenticatedShellTasks.js';
import { permissionStore } from '../stores/permissions.svelte.js';
import { ssoStore } from '../stores/sso.svelte.js';
import { workspacePermissions } from '../stores/workspacePermissions.svelte.js';
import { workspacesStore } from '../stores/workspaces.svelte.js';

beforeEach(() => {
  vi.clearAllMocks();
});

describe('authenticated shell bootstrap', () => {
  test('starts deferred probes without waiting for the critical path', async () => {
    let releaseCritical;
    const events = [];
    const critical = new Promise((resolve) => {
      releaseCritical = resolve;
    });

    const bootstrap = runAuthenticatedShellBootstrap({
      userId: 7,
      criticalTasks: [
        () => {
          events.push('critical');
          return critical;
        },
      ],
      deferredTasks: [
        () => {
          events.push('deferred');
        },
      ],
    });

    await vi.waitFor(() => expect(events).toEqual(['critical', 'deferred']));
    releaseCritical();
    const result = await bootstrap;
    await result.deferredPromise;

    expect(result.metrics.criticalRequestCount).toBe(1);
    expect(result.metrics.deferredRequestCount).toBe(1);
    expect(result.metrics.criticalDurationMs).toBeLessThan(AUTHENTICATED_SHELL_BOOTSTRAP_BUDGET_MS);
    expect(result.metrics.withinBudget).toBe(true);
  });

  test('settles optional failures without rejecting shell readiness', async () => {
    const result = await runAuthenticatedShellBootstrap({
      userId: 8,
      criticalTasks: [() => Promise.resolve('ready')],
      deferredTasks: [() => Promise.reject(new Error('feature disabled'))],
    });

    await expect(result.deferredPromise).resolves.toEqual([
      expect.objectContaining({ status: 'rejected' }),
    ]);
    expect(result.criticalResults).toEqual([
      expect.objectContaining({ status: 'fulfilled', value: 'ready' }),
    ]);
  });
});

describe('authenticated shell task grouping', () => {
  test('keeps the workspace directory off the shell critical path', async () => {
    const user = { id: 7 };
    const { criticalTasks, deferredTasks } = buildAuthenticatedShellTasks({
      user,
      userId: 7,
      onEmailVerificationChange: vi.fn(),
    });

    await Promise.all(criticalTasks.map((task) => task()));

    expect(workspacesStore.load).not.toHaveBeenCalled();
    expect(workspacesStore.loadPersonalWorkspace).not.toHaveBeenCalled();
    expect(permissionStore.loadAllPermissions).toHaveBeenCalledWith(user);
    expect(permissionStore.loadUserPermissions).toHaveBeenCalledWith(7);
    expect(workspacePermissions.loadPermissions).toHaveBeenCalledWith(7);

    await Promise.all(deferredTasks.map((task) => task()));

    expect(workspacesStore.load).toHaveBeenCalledTimes(1);
    expect(workspacesStore.loadPersonalWorkspace).toHaveBeenCalledTimes(1);
    expect(loadAuthenticatedShellUI).toHaveBeenCalledWith(7);
    expect(ssoStore.getVerificationStatus).toHaveBeenCalledTimes(1);
  });

  test('skips the per-user permission probes without a user id', async () => {
    const { criticalTasks, deferredTasks } = buildAuthenticatedShellTasks({
      user: null,
      userId: undefined,
      onEmailVerificationChange: vi.fn(),
    });

    await Promise.all(criticalTasks.map((task) => task()));

    expect(permissionStore.loadAllPermissions).toHaveBeenCalledWith(null);
    expect(permissionStore.loadUserPermissions).not.toHaveBeenCalled();
    expect(workspacePermissions.loadPermissions).not.toHaveBeenCalled();

    await Promise.all(deferredTasks.map((task) => task()));
    expect(loadAuthenticatedShellUI).toHaveBeenCalledWith(undefined);
  });
});
