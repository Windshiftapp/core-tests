import { beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('../api.js', () => ({
  api: {
    shellBootstrap: { get: vi.fn() },
    themes: { getActive: vi.fn() },
  },
}));

vi.mock('../stores/attachmentStatus.svelte.js', () => ({
  attachmentStatus: { hydrate: vi.fn() },
}));
vi.mock('../stores/aiStore.svelte.js', () => ({
  aiStore: { hydrate: vi.fn() },
}));
vi.mock('../stores/capabilities.svelte.js', () => ({
  capabilitiesStore: { hydrate: vi.fn() },
}));
vi.mock('../stores/logbook.svelte.js', () => ({
  logbookStore: { hydrateAvailability: vi.fn() },
}));
vi.mock('../stores/workItemStalenessSettings.svelte.js', () => ({
  workItemStalenessSettings: { hydrate: vi.fn() },
}));
vi.mock('../stores/moduleSettings.js', () => ({
  moduleSettings: { hydrate: vi.fn() },
}));
vi.mock('../stores/permissions.svelte.js', () => ({
  permissionStore: {
    setLogbookAvailable: vi.fn(),
    setHasAssetSets: vi.fn(),
    setHasActivePortals: vi.fn(),
    setManagesChannels: vi.fn(),
  },
}));
vi.mock('../stores/theme.svelte.js', () => ({
  themeStore: { setActiveTheme: vi.fn() },
}));
vi.mock('../stores/workspaceDataStore.svelte.js', () => ({
  workspaceDataStore: { refresh: vi.fn() },
}));
vi.mock('../stores/workspaces.svelte.js', () => ({
  workspacesStore: { reload: vi.fn() },
}));

import { api } from '../api.js';
import { aiStore } from '../stores/aiStore.svelte.js';
import { attachmentStatus } from '../stores/attachmentStatus.svelte.js';
import { capabilitiesStore } from '../stores/capabilities.svelte.js';
import { logbookStore } from '../stores/logbook.svelte.js';
import { moduleSettings } from '../stores/moduleSettings.js';
import { permissionStore } from '../stores/permissions.svelte.js';
import { themeStore } from '../stores/theme.svelte.js';
import { workItemStalenessSettings } from '../stores/workItemStalenessSettings.svelte.js';
import { workspaceDataStore } from '../stores/workspaceDataStore.svelte.js';
import { workspacesStore } from '../stores/workspaces.svelte.js';
import {
  hydrateAuthenticatedShellUI,
  refreshAuthenticatedShellUI,
} from './authenticatedShellUI.js';

const snapshot = {
  module_settings: { test_management_enabled: false },
  attachment_status: { enabled: true, writable: true },
  ai: { available: true, chat_enabled: true },
  features: {
    capabilities: ['plugin.example'],
    logbook_available: true,
  },
  has_asset_sets: true,
  has_active_portals: false,
  manages_channels: true,
  work_item_staleness: { stale_after_days: 30 },
};

beforeEach(() => {
  vi.clearAllMocks();
  api.shellBootstrap.get.mockResolvedValue(snapshot);
  api.themes.getActive.mockResolvedValue({ id: 9, name: 'Night' });
  workspacesStore.reload.mockResolvedValue([]);
  workspaceDataStore.refresh.mockResolvedValue();
});

describe('authenticated shell UI refresh', () => {
  test('hydrates every shared shell capability from one snapshot', () => {
    expect(hydrateAuthenticatedShellUI(snapshot)).toBe(true);

    expect(moduleSettings.hydrate).toHaveBeenCalledWith(snapshot.module_settings);
    expect(attachmentStatus.hydrate).toHaveBeenCalledWith(snapshot.attachment_status);
    expect(aiStore.hydrate).toHaveBeenCalledWith(snapshot.ai);
    expect(capabilitiesStore.hydrate).toHaveBeenCalledWith(snapshot.features);
    expect(logbookStore.hydrateAvailability).toHaveBeenCalledWith(true);
    expect(workItemStalenessSettings.hydrate).toHaveBeenCalledWith(snapshot.work_item_staleness);
    expect(permissionStore.setLogbookAvailable).toHaveBeenCalledWith(true);
    expect(permissionStore.setHasAssetSets).toHaveBeenCalledWith(true);
    expect(permissionStore.setHasActivePortals).toHaveBeenCalledWith(false);
    expect(permissionStore.setManagesChannels).toHaveBeenCalledWith(true);
  });

  test('refreshes the shell, theme, workspace catalog, and active workspace data together', async () => {
    await expect(refreshAuthenticatedShellUI()).resolves.toBe(true);

    expect(api.shellBootstrap.get).toHaveBeenCalledTimes(1);
    expect(api.themes.getActive).toHaveBeenCalledTimes(1);
    expect(workspacesStore.reload).toHaveBeenCalledTimes(1);
    expect(workspaceDataStore.refresh).toHaveBeenCalledTimes(1);
    expect(aiStore.hydrate).toHaveBeenCalledWith(snapshot.ai);
    expect(themeStore.setActiveTheme).toHaveBeenCalledWith({ id: 9, name: 'Night' });
  });
});
