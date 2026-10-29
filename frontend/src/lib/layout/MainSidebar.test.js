import { render, screen, waitFor } from '@testing-library/svelte';
import { tick } from 'svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  getSnapshot: vi.fn(),
  searchWorkspaces: vi.fn(),
}));

vi.mock('../router.js', async () => {
  const { writable } = await import('svelte/store');
  return {
    currentRoute: writable({ view: 'dashboard', path: '/', params: {} }),
    isWorkspaceRoute: vi.fn(() => false),
  };
});

vi.mock('../stores/permissions.svelte.js', async () => {
  const { writable } = await import('svelte/store');
  return { permissionStore: writable({}) };
});

vi.mock('../stores/ui.svelte.js', async () => {
  const { writable } = await import('svelte/store');
  const uiStore = writable({ navExpanded: true });
  uiStore.toggleNavExpanded = vi.fn();
  return { uiStore };
});

vi.mock('../stores/workspaces.svelte.js', async () => {
  const { writable } = await import('svelte/store');
  const workspacesStore = writable({
    regularWorkspaces: [
      { id: 1, name: 'Alpha', active: true },
      { id: 2, name: 'Bravo', active: true },
    ],
    truncated: false,
    loaded: true,
  });
  workspacesStore.searchWorkspaces = mocks.searchWorkspaces;
  return { workspacesStore };
});

vi.mock('../stores/homepageStore.svelte.js', () => ({
  homepageStore: { getSnapshot: mocks.getSnapshot },
}));

vi.mock('../stores/i18n.svelte.js', () => ({ t: (key) => key }));

vi.mock('../stores/theme.svelte.js', () => ({
  themeStore: { isDarkMode: false, activeTheme: null },
}));

vi.mock('../stores/aiStore.svelte.js', () => ({
  aiStore: { chatAvailable: false },
}));

vi.mock('../utils/keyboardShortcuts.js', () => ({ getShortcutDisplay: () => '' }));
vi.mock('../utils/isTauri.js', () => ({ isTauri: () => false }));
vi.mock('../navigation/mainNavigation.js', () => ({ mainNavItems: [], bottomNavItems: [] }));

// The footer surfaces are unrelated to workspace ordering and pull in heavy
// stores, so stub them out.
vi.mock('../components/UserAvatar.svelte', () => ({
  default: function MockUserAvatar() {
    return {};
  },
}));
vi.mock('../features/notifications/NotificationTray.svelte', () => ({
  default: function MockNotificationTray() {
    return {};
  },
}));

import MainSidebar from './MainSidebar.svelte';

function renderSidebar(activeSurface = null) {
  return render(MainSidebar, {
    props: {
      activeSurface,
      onSurfaceChange: () => {},
      onShowCommandPalette: () => {},
      onShowCreateModal: () => {},
      onShowChatPanel: () => {},
      onToggleTerminal: () => {},
    },
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  mocks.getSnapshot.mockResolvedValue({
    recent_workspaces: [
      { workspace_id: 2, last_visited: '2024-01-02T00:00:00Z' },
      { workspace_id: 1, last_visited: '2024-01-01T00:00:00Z' },
    ],
  });
});

describe('MainSidebar workspace recency', () => {
  it('loads the recent workspace order on mount, before the menu is opened', async () => {
    renderSidebar();

    await waitFor(() => expect(mocks.getSnapshot).toHaveBeenCalled());
  });

  it('opens the menu already sorted by recency', async () => {
    const { rerender } = renderSidebar();
    await waitFor(() => expect(mocks.getSnapshot).toHaveBeenCalled());
    // Let the preloaded order settle before the menu becomes visible.
    await tick();

    await rerender({ activeSurface: 'workspaces' });

    const items = screen.getAllByTestId('workspace-dropdown-item');
    expect(items.map((item) => item.textContent.trim())).toEqual(['Bravo', 'Alpha']);
  });
});
