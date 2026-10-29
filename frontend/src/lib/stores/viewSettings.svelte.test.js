import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api/collections.js', () => ({
  collections: {
        getBoardConfiguration: vi.fn(),
      },
  collectionCategories: { getAll: vi.fn().mockResolvedValue([]) },
}));

const collectionMocks = vi.hoisted(() => ({ peekBoardConfiguration: vi.fn() }));
vi.mock('./collectionContext.js', () => ({
  collectionStore: { peekBoardConfiguration: collectionMocks.peekBoardConfiguration },
}));

import { collections } from '../api/collections.js';
import { viewSettingsStore } from './viewSettings.svelte.js';

describe('view settings store', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    collectionMocks.peekBoardConfiguration.mockReturnValue(null);
    viewSettingsStore.invalidateWorkspace(1);
    viewSettingsStore.invalidateWorkspace(2);
    viewSettingsStore.invalidateWorkspace(3);
    viewSettingsStore.invalidateWorkspace(4);
  });

  it('exposes every toggleable nav entry before a lookup and on failure', async () => {
    // The fallback covers tools entries too; view membership checks only
    // ever match the view ids among them.
    expect(viewSettingsStore.enabledViewIds(1, null)).toEqual(viewSettingsStore.allNavIds);

    collections.getBoardConfiguration.mockRejectedValue(new Error('boom'));
    await viewSettingsStore.load(1, null);

    expect(viewSettingsStore.enabledViewIds(1, null)).toEqual(viewSettingsStore.allNavIds);
    expect(viewSettingsStore.entryFor(1, null).loaded).toBe(false);
  });

  it('caches the effective override per scope', async () => {
    collections.getBoardConfiguration.mockResolvedValue({
      view_settings: { enabled_views: ['board', 'list'] },
      view_settings_inherited: false,
    });

    await viewSettingsStore.load(1, 7);

    expect(collections.getBoardConfiguration).toHaveBeenCalledWith(7, 1);
    expect(viewSettingsStore.enabledViewIds(1, 7)).toEqual(['board', 'list']);
    expect(viewSettingsStore.inherited(1, 7)).toBe(false);

    await viewSettingsStore.load(1, 7);
    expect(collections.getBoardConfiguration).toHaveBeenCalledTimes(1);
  });

  it('reuses the collection view board configuration instead of a plain read', async () => {
    collectionMocks.peekBoardConfiguration.mockReturnValue({
      config: {
        view_settings: { enabled_views: ['board', 'list'] },
        view_settings_inherited: false,
      },
    });

    await viewSettingsStore.load(3, 7);

    expect(collections.getBoardConfiguration).not.toHaveBeenCalled();
    expect(viewSettingsStore.enabledViewIds(3, 7)).toEqual(['board', 'list']);
  });

  it('joins an in-flight board bootstrap for the same scope', async () => {
    let resolveBootstrap;
    collectionMocks.peekBoardConfiguration.mockReturnValue({
      pending: new Promise((resolve) => {
        resolveBootstrap = resolve;
      }),
    });

    const loading = viewSettingsStore.load(4, 8);
    await Promise.resolve();
    resolveBootstrap({
      view_settings: { enabled_views: ['backlog'] },
      view_settings_inherited: true,
    });
    await loading;

    expect(collections.getBoardConfiguration).not.toHaveBeenCalled();
    expect(viewSettingsStore.enabledViewIds(4, 8)).toEqual(['backlog']);
  });

  it('falls back to every view for missing and empty settings', async () => {
    collections.getBoardConfiguration
      .mockResolvedValueOnce({ view_settings_inherited: true })
      .mockResolvedValueOnce({ view_settings: { enabled_views: [] } });

    await viewSettingsStore.load(2, null);
    expect(viewSettingsStore.enabledViewIds(2, null)).toEqual(viewSettingsStore.allNavIds);
    expect(viewSettingsStore.inherited(2, null)).toBe(true);

    await viewSettingsStore.load(2, 9);
    expect(viewSettingsStore.enabledViewIds(2, 9)).toEqual(viewSettingsStore.allNavIds);
  });

  it('exposes the workspace nav set for tools entries', async () => {
    collections.getBoardConfiguration.mockResolvedValue({
      view_settings: { enabled_views: ['board', 'agents'] },
    });

    // Unloaded scopes resolve to everything enabled, agents included.
    expect(viewSettingsStore.enabledNavIds(1)).toContain('agents');
    await viewSettingsStore.load(1, null);

    expect(viewSettingsStore.enabledNavIds(1)).toEqual(['board', 'agents']);
    // Other workspaces keep the everything-enabled fallback until their own
    // lookup resolves.
    expect(viewSettingsStore.enabledNavIds(2)).toEqual(viewSettingsStore.allNavIds);
    // Test entries are not nav-configurable and never appear in the set.
    expect(viewSettingsStore.allNavIds).not.toContain('test-cases');
  });

  it('applies a saved set immediately and merges inherited workspace-only ids', async () => {
    collections.getBoardConfiguration.mockResolvedValue({
      view_settings: { enabled_views: ['board', 'list', 'queue', 'agents'] },
    });
    await viewSettingsStore.load(1, null);

    // A workspace-scope write lands in registry order without a refetch.
    viewSettingsStore.applyEnabledViews(1, null, ['agents', 'board']);
    expect(viewSettingsStore.enabledNavIds(1)).toEqual(['board', 'agents']);
    expect(viewSettingsStore.inherited(1)).toBe(false);

    // Collection overrides keep the workspace-only ids inherited underneath.
    viewSettingsStore.applyEnabledViews(1, 7, ['list'], true);
    expect(viewSettingsStore.enabledViewIds(1, 7)).toEqual(['list', 'agents']);
    expect(viewSettingsStore.inherited(1, 7)).toBe(true);
  });

  it('invalidates a single scope and whole workspaces independently', async () => {
    collections.getBoardConfiguration.mockResolvedValue({
      view_settings: { enabled_views: ['board'] },
    });
    await viewSettingsStore.load(1, null);
    await viewSettingsStore.load(1, 7);
    await viewSettingsStore.load(2, null);
    collections.getBoardConfiguration.mockClear();

    viewSettingsStore.invalidate(1, 7);
    await viewSettingsStore.load(1, 7);
    expect(collections.getBoardConfiguration).toHaveBeenCalledTimes(1);

    collections.getBoardConfiguration.mockClear();
    viewSettingsStore.invalidateWorkspace(1);
    await viewSettingsStore.load(1, null);
    await viewSettingsStore.load(1, 7);
    expect(collections.getBoardConfiguration).toHaveBeenCalledTimes(2);

    // The other workspace's caches survive.
    collections.getBoardConfiguration.mockClear();
    await viewSettingsStore.load(2, null);
    expect(collections.getBoardConfiguration).not.toHaveBeenCalled();
  });
});

describe('disabled view redirect', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    viewSettingsStore.invalidateWorkspace(1);
  });

  it('ignores non-workspace and unguarded views', async () => {
    const { getDisabledViewRedirect } = await import('../pages/mainAppViewGuard.js');
    expect(getDisabledViewRedirect({ view: 'workspace-settings-general', params: { id: 1 } }, {})).toBeNull();
    expect(getDisabledViewRedirect({ view: 'workspace-agents', params: { id: 1 } }, {})).toBeNull();
    expect(getDisabledViewRedirect({ view: 'workspace-board', params: {} }, {})).toBeNull();
  });

  it('does not redirect disabled workspace-only nav entries', async () => {
    const { getDisabledViewRedirect } = await import('../pages/mainAppViewGuard.js');
    collections.getBoardConfiguration.mockResolvedValue({
      view_settings: { enabled_views: ['board'] },
    });
    await viewSettingsStore.load(1, null);

    // Tools entries are visibility toggles: hiding one from the sidebar must
    // not make its route unreachable. This keeps linked pages from item link
    // panels, notifications, and search working.
    expect(getDisabledViewRedirect({ view: 'workspace-agents', params: { id: 1 } }, {})).toBeNull();
    expect(getDisabledViewRedirect({ view: 'workspace-analytics', params: { id: 1 } }, {})).toBeNull();
    expect(getDisabledViewRedirect({ view: 'workspace-pages', params: { id: 1 } }, {})).toBeNull();
    expect(
      getDisabledViewRedirect({ view: 'workspace-pages', params: { id: 1, pageId: 9 } }, {})
    ).toBeNull();
    // Test routes are not guarded either: test entries are not configurable.
    expect(getDisabledViewRedirect({ view: 'test-cases', params: { id: 1 } }, {})).toBeNull();
    // Detail views of a tools entry are not themselves guarded.
    expect(getDisabledViewRedirect({ view: 'workspace-agent-profile', params: { id: 1 } }, {})).toBeNull();
  });

  it('keeps enabled views and redirects disabled ones to the default view', async () => {
    const { getDisabledViewRedirect } = await import('../pages/mainAppViewGuard.js');
    collections.getBoardConfiguration.mockResolvedValue({
      view_settings: { enabled_views: ['board', 'list'] },
    });
    await viewSettingsStore.load(1, null);

    const route = { view: 'workspace-board', params: { id: 1 } };
    expect(getDisabledViewRedirect(route, { default_view: 'board' })).toBeNull();

    // The disabled map route falls back to the enabled workspace default.
    expect(
      getDisabledViewRedirect({ view: 'workspace-map', params: { id: 1 } }, { default_view: 'board' })
    ).toBe('/workspaces/1/board');
  });

  it('falls back to the first enabled view when the default is disabled', async () => {
    const { getDisabledViewRedirect } = await import('../pages/mainAppViewGuard.js');
    collections.getBoardConfiguration.mockResolvedValue({
      view_settings: { enabled_views: ['list'] },
    });
    await viewSettingsStore.load(1, 7);

    expect(
      getDisabledViewRedirect(
        { view: 'workspace-board', params: { id: 1, collectionId: 7 } },
        { default_view: 'board' }
      )
    ).toBe('/workspaces/1/collections/7/list');
  });
});
