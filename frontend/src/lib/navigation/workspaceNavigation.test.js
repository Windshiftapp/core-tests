import { describe, expect, test } from 'vitest';
import { COLLECTION_VIEW_IDS, collectionSwitchUrl, visibleWorkspaceSettingsItems, workspaceOnlyViews, workspaceSettingsItems, workspaceSettingsRoute, workspaceViewItems } from './workspaceNavigation.js';

describe('Agent Studio workspace navigation', () => {
  test('keeps Agents in the workspace tools group', () => {
    expect(workspaceViewItems.some((item) => item.id === 'agents')).toBe(false);
    expect(workspaceOnlyViews).toContainEqual(
      expect.objectContaining({
        id: 'agents',
        labelKey: 'users.agents.title',
        tooltipKey: 'users.agents.description',
        testId: 'workspace-nav-agents',
        activeViews: ['workspace-agents', 'workspace-agent-profile', 'workspace-agent-create'],
      })
    );
    expect(workspaceSettingsItems.some((item) => item.id === 'coding-agents')).toBe(false);
  });

  test('renders the queue in the views group as a collection-routed view', () => {
    // The queue trails the board views in the sidebar's views group.
    expect(workspaceViewItems.at(-1)).toEqual(
      expect.objectContaining({
        id: 'queue',
        labelKey: 'supportQueue.title',
        tooltipKey: 'supportQueue.description',
        testId: 'workspace-nav-queue',
        activeViews: ['workspace-queue'],
      })
    );
    // Its route follows the selected collection, but the visibility toggle
    // stays in the workspace nav set rather than the collection view set.
    expect(workspaceOnlyViews.some((item) => item.id === 'queue')).toBe(false);
    expect(COLLECTION_VIEW_IDS.has('queue')).toBe(false);
  });
});

describe('workspace settings module registry', () => {
  test('action-credentials module is registered with its route and permission gate', () => {
    const item = workspaceSettingsItems.find((i) => i.id === 'action-credentials');
    expect(item).toEqual(
      expect.objectContaining({
        view: 'workspace-settings-action-credentials',
        permission: 'action.credential.manage',
      })
    );
    expect(workspaceSettingsRoute(7, 'action-credentials')).toBe(
      '/workspaces/7/settings/action-credentials'
    );
  });

  test('visibleWorkspaceSettingsItems filters on the per-module permission', () => {
    const allowAll = () => true;
    expect(visibleWorkspaceSettingsItems(1, allowAll).map((i) => i.id)).toEqual(
      workspaceSettingsItems.map((i) => i.id)
    );

    // A holder of workspace.admin without action.credential.manage loses only
    // the gated module — every other settings tab stays visible.
    const denyCredentialManage = (workspaceId, permission) =>
      permission !== 'action.credential.manage';
    const visible = visibleWorkspaceSettingsItems(1, denyCredentialManage).map(
      (i) => i.id
    );
    expect(visible).not.toContain('action-credentials');
    expect(visible).toEqual(
      workspaceSettingsItems.filter((i) => !i.permission).map((i) => i.id)
    );

    // The gate is per workspace: the same accessor consulted for another
    // workspace can see the module there.
    const gatedByWorkspace = (workspaceId, permission) =>
      permission !== 'action.credential.manage' || workspaceId === 2;
    expect(
      visibleWorkspaceSettingsItems(2, gatedByWorkspace).map((i) => i.id)
    ).toContain('action-credentials');
    expect(
      visibleWorkspaceSettingsItems(3, gatedByWorkspace).map((i) => i.id)
    ).not.toContain('action-credentials');
  });
});

describe('collectionSwitchUrl', () => {
  const cases = [
    // Collection-scoped views keep their view when the collection changes.
    {
      name: 'board config keeps its compound path',
      route: { path: '/workspaces/2/board/configure', view: 'workspace-board-config' },
      collection: 4,
      want: '/workspaces/2/collections/4/board/configure',
    },
    {
      name: 'board config re-scopes an existing collection path',
      route: { path: '/workspaces/2/collections/9/board/configure', view: 'workspace-board-config' },
      collection: 4,
      want: '/workspaces/2/collections/4/board/configure',
    },
    {
      name: 'board config drops the collection for all items',
      route: { path: '/workspaces/2/collections/9/board/configure', view: 'workspace-board-config' },
      collection: null,
      want: '/workspaces/2/board/configure',
    },
    {
      name: 'nav config keeps its path',
      route: { path: '/workspaces/2/collections/9/nav-config', view: 'workspace-nav-config' },
      collection: 4,
      want: '/workspaces/2/collections/4/nav-config',
    },
    {
      name: 'queue keeps its path',
      route: { path: '/workspaces/2/queue', view: 'workspace-queue' },
      collection: 4,
      want: '/workspaces/2/collections/4/queue',
    },
    {
      name: 'overview keeps its path',
      route: { path: '/workspaces/2/overview', view: 'workspace-overview' },
      collection: 4,
      want: '/workspaces/2/collections/4/overview',
    },
    {
      name: 'workspace detail becomes the collection root',
      route: { path: '/workspaces/2', view: 'workspace-detail' },
      collection: 4,
      want: '/workspaces/2/collections/4',
    },
    {
      name: 'workspace detail drops the collection for all items',
      route: { path: '/workspaces/2/collections/9', view: 'workspace-detail' },
      collection: null,
      want: '/workspaces/2',
    },
    {
      name: 'item detail keeps the item id',
      route: { path: '/workspaces/2/items/7', view: 'item-detail' },
      collection: 4,
      want: '/workspaces/2/collections/4/items/7',
    },
    // Workspace-only views have no collection route: fall back to the default.
    {
      name: 'settings falls back to the default view',
      route: { path: '/workspaces/2/settings/general', view: 'workspace-settings-general' },
      collection: 4,
      want: '/workspaces/2/collections/4/board',
    },
    {
      name: 'agent profile falls back to the default view',
      route: { path: '/workspaces/2/agents/7', view: 'workspace-agent-profile' },
      collection: 4,
      want: '/workspaces/2/collections/4/board',
    },
    {
      name: 'archived pages fall back to the default view',
      route: { path: '/workspaces/2/pages/archived', view: 'workspace-pages-archived' },
      collection: 4,
      want: '/workspaces/2/collections/4/board',
    },
    {
      name: 'calendar falls back to the default view',
      route: { path: '/workspaces/2/calendar', view: 'workspace-calendar' },
      collection: 4,
      want: '/workspaces/2/collections/4/board',
    },
    {
      name: 'test views fall back to the default view',
      route: { path: '/workspaces/2/tests/cases/7', view: 'test-case-detail' },
      collection: 4,
      want: '/workspaces/2/collections/4/board',
    },
    {
      name: 'workspace-only views keep their path for all items',
      route: { path: '/workspaces/2/settings/general', view: 'workspace-settings-general' },
      collection: null,
      want: '/workspaces/2/settings/general',
    },
    {
      name: 'uses the supplied default view',
      route: { path: '/workspaces/2/agents', view: 'workspace-agents' },
      collection: 4,
      defaultView: 'list',
      want: '/workspaces/2/collections/4/list',
    },
  ];

  test.each(cases)('$name', ({ route, collection, defaultView, want }) => {
    expect(collectionSwitchUrl(route, 2, collection, defaultView ?? 'board')).toBe(want);
  });
});
