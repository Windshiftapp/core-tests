import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { writable } from 'svelte/store';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  getBoardConfiguration: vi.fn(),
  updateBoardConfiguration: vi.fn(),
  createBoardConfiguration: vi.fn(),
  getCollection: vi.fn(),
  getWorkspace: vi.fn(),
}));

vi.mock('../api/collections.js', () => ({
  collections: {
        getBoardConfiguration: mocks.getBoardConfiguration,
        updateBoardConfiguration: mocks.updateBoardConfiguration,
        createBoardConfiguration: mocks.createBoardConfiguration,
        get: mocks.getCollection,
      },
  collectionCategories: { getAll: vi.fn().mockResolvedValue([]) },
}))

vi.mock('../api/workspaces.js', () => ({
  workspaces: {
        get: mocks.getWorkspace,
      },
}));

vi.mock('../stores/workspaces.svelte.js', () => ({
  currentWorkspace: writable({ name: 'WS One' }),
}))

vi.mock('../stores/workspacePermissions.svelte.js', () => ({
  workspacePermissions: { canAdminWorkspace: vi.fn(() => true) },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('../stores/toasts.svelte.js', () => ({
  errorToast: vi.fn(),
}));

vi.mock('../stores/moduleSettings.js', () => ({
  moduleSettings: writable({ test_management_enabled: true }),
}));

vi.mock('../stores/viewSettings.svelte.js', () => ({
  viewSettingsStore: {
    load: vi.fn().mockResolvedValue(undefined),
    allNavIds: [
      'backlog', 'board', 'list', 'tree', 'map', 'roadmap',
      'queue', 'agents', 'iterations', 'milestones', 'analytics', 'actions', 'pages',
    ],
    invalidate: vi.fn(),
    invalidateWorkspace: vi.fn(),
    applyEnabledViews: vi.fn(),
  },
}));

import NavigationConfigPage from './NavigationConfigPage.svelte';
import { viewSettingsStore } from '../stores/viewSettings.svelte.js';
import { collections } from '../api/collections.js';
import { workspaces } from '../api/workspaces.js';


const COLLECTION_VIEW_IDS = ['backlog', 'board', 'list', 'tree', 'map', 'roadmap'];
const QUEUE_ID = 'queue';
const WORKSPACE_ONLY_IDS = ['agents', 'iterations', 'milestones', 'analytics', 'actions', 'pages'];

// A stored collection config whose effective set inherits workspace-only
// tools ids from the workspace row, as the backend merges them into every
// collection-scope read.
function collectionConfigResponse() {
  return {
    id: 42,
    columns: [],
    view_settings: {
      enabled_views: [...COLLECTION_VIEW_IDS, 'queue', 'milestones'],
    },
    view_settings_inherited: false,
  };
}

function boardConfigRow(id) {
  return screen.getByTestId(`nav-config-row-${id}`);
}

beforeEach(() => {
  mocks.updateBoardConfiguration.mockImplementation((_collectionId, _configId, payload) =>
    Promise.resolve({ ...collectionConfigResponse(), ...payload }));
});

afterEach(() => {
  document.body.innerHTML = '';
  vi.clearAllMocks();
});

describe('NavigationConfigPage collection scope', () => {
  test('renders views and fixed sections but no workspace-only tool rows', async () => {
    mocks.getBoardConfiguration.mockResolvedValue(collectionConfigResponse());
    mocks.getCollection.mockResolvedValue({ id: 7, name: 'Alpha' });

    render(NavigationConfigPage, { props: { workspaceId: 1, collectionId: 7 } });

    await waitFor(() => expect(boardConfigRow('board')).toBeInTheDocument());
    expect(boardConfigRow('overview')).toBeInTheDocument();
    expect(boardConfigRow('test-cases')).toBeInTheDocument();

    // Workspace-only tools cannot be toggled at collection scope, so the
    // screen must not offer them at all — and neither the workspace-scoped
    // queue view nor the fixed workspace affordances.
    expect(screen.queryByTestId('nav-config-section-tools')).not.toBeInTheDocument();
    for (const id of [...WORKSPACE_ONLY_IDS, QUEUE_ID]) {
      expect(screen.queryByTestId(`nav-config-row-${id}`)).not.toBeInTheDocument();
    }
  });

  test('toggling a view saves only collection view ids despite inherited tools ids', async () => {
    mocks.getBoardConfiguration.mockResolvedValue(collectionConfigResponse());
    mocks.getCollection.mockResolvedValue({ id: 7, name: 'Alpha' });

    render(NavigationConfigPage, { props: { workspaceId: 1, collectionId: 7 } });
    await waitFor(() => expect(boardConfigRow('board')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('nav-config-toggle-list'));

    await waitFor(() => expect(mocks.updateBoardConfiguration).toHaveBeenCalledTimes(1));
    const [, , payload] = mocks.updateBoardConfiguration.mock.calls[0];
    expect(payload.view_settings.enabled_views).toEqual(
      COLLECTION_VIEW_IDS.filter((id) => id !== 'list'),
    );
    // The saved set reaches the nav store immediately so the sidebar reacts
    // without a refetch.
    await waitFor(() =>
      expect(viewSettingsStore.applyEnabledViews).toHaveBeenCalledWith(
        1,
        7,
        COLLECTION_VIEW_IDS.filter((id) => id !== 'list'),
        false,
      )
    );
  });

  test('a failed save does not discard a later toggle', async () => {
    mocks.getBoardConfiguration.mockResolvedValue(collectionConfigResponse());
    mocks.getCollection.mockResolvedValue({ id: 7, name: 'Alpha' });
    mocks.updateBoardConfiguration
      .mockRejectedValueOnce(new Error('offline'))
      .mockImplementation((_collectionId, _configId, payload) =>
        Promise.resolve({ ...collectionConfigResponse(), ...payload })
      );

    render(NavigationConfigPage, { props: { workspaceId: 1, collectionId: 7 } });
    await waitFor(() => expect(boardConfigRow('board')).toBeInTheDocument());

    // Toggle two views in quick succession; the first save fails.
    fireEvent.click(screen.getByTestId('nav-config-toggle-list'));
    fireEvent.click(screen.getByTestId('nav-config-toggle-tree'));

    await waitFor(() => expect(mocks.updateBoardConfiguration).toHaveBeenCalledTimes(2));
    const [, , secondPayload] = mocks.updateBoardConfiguration.mock.calls[1];
    // The later toggle must survive the earlier failure and be persisted.
    expect(secondPayload.view_settings.enabled_views).not.toContain('list');
    expect(secondPayload.view_settings.enabled_views).not.toContain('tree');
  });

  test('resetting sends a null enabled_views override', async () => {
    mocks.getBoardConfiguration.mockResolvedValue(collectionConfigResponse());
    mocks.getCollection.mockResolvedValue({ id: 7, name: 'Alpha' });

    render(NavigationConfigPage, { props: { workspaceId: 1, collectionId: 7 } });
    await waitFor(() => expect(boardConfigRow('board')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('nav-config-reset'));

    await waitFor(() => expect(mocks.updateBoardConfiguration).toHaveBeenCalledTimes(1));
    const [, , payload] = mocks.updateBoardConfiguration.mock.calls[0];
    expect(payload.view_settings.enabled_views).toBeNull();
  });

  test('reset applies the re-inherited set to the nav store', async () => {
    mocks.getBoardConfiguration
      .mockResolvedValueOnce(collectionConfigResponse())
      .mockResolvedValueOnce({
        id: 42,
        columns: [],
        view_settings_inherited: true,
      });
    mocks.getCollection.mockResolvedValue({ id: 7, name: 'Alpha' });

    render(NavigationConfigPage, { props: { workspaceId: 1, collectionId: 7 } });
    await waitFor(() => expect(boardConfigRow('board')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('nav-config-reset'));

    await waitFor(() =>
      expect(viewSettingsStore.applyEnabledViews).toHaveBeenCalledWith(
        1,
        7,
        COLLECTION_VIEW_IDS,
        true,
      )
    );
  });
});

describe('NavigationConfigPage workspace scope', () => {
  test('renders the workspace-only tools section with toggles', async () => {
    mocks.getBoardConfiguration.mockResolvedValue({ id: 5, columns: [] });
    mocks.getWorkspace.mockResolvedValue({ id: 1, name: 'WS One', default_view: 'board' });

    render(NavigationConfigPage, { props: { workspaceId: 1, collectionId: null } });

    await waitFor(() => expect(boardConfigRow('board')).toBeInTheDocument());
    expect(screen.getByTestId('nav-config-section-tools')).toBeInTheDocument();
    // The queue renders in the views group as a workspace-scoped view.
    expect(boardConfigRow(QUEUE_ID)).toBeInTheDocument();
    expect(screen.getByTestId(`nav-config-toggle-${QUEUE_ID}`)).toBeInTheDocument();
    for (const id of WORKSPACE_ONLY_IDS) {
      expect(boardConfigRow(id)).toBeInTheDocument();
      expect(screen.getByTestId(`nav-config-toggle-${id}`)).toBeInTheDocument();
    }
  });

  test('toggling a tools entry applies the workspace set to the nav store', async () => {
    mocks.getBoardConfiguration.mockResolvedValue({ id: 5, columns: [] });
    mocks.getWorkspace.mockResolvedValue({ id: 1, name: 'WS One', default_view: 'board' });

    render(NavigationConfigPage, { props: { workspaceId: 1, collectionId: null } });
    await waitFor(() => expect(boardConfigRow('agents')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('nav-config-toggle-agents'));

    await waitFor(() => expect(viewSettingsStore.applyEnabledViews).toHaveBeenCalledTimes(1));
    const [workspaceId, collectionId, views] = viewSettingsStore.applyEnabledViews.mock.calls[0];
    expect(workspaceId).toBe(1);
    expect(collectionId).toBeNull();
    expect(views).not.toContain('agents');
    expect(views).toContain('board');
  });
});
