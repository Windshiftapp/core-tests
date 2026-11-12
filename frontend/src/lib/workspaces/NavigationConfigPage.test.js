import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { writable } from 'svelte/store';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  getBoardConfiguration: vi.fn(),
  patchBoardConfiguration: vi.fn(),
  getCollection: vi.fn(),
  getWorkspace: vi.fn(),
}));

vi.mock('../api/collections.js', () => ({
  collections: {
        getBoardConfiguration: mocks.getBoardConfiguration,
        patchBoardConfiguration: mocks.patchBoardConfiguration,
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
  surfaceError: vi.fn(),
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
// collection-scope read. The columns carry the server-managed fields the
// full-replace endpoint rejects; the merge patch must never send them.
function collectionConfigResponse() {
  return {
    id: 42,
    columns: [
      {
        id: 9,
        board_configuration_id: 42,
        name: 'In progress',
        display_order: 0,
        wip_limit: null,
        color: '#123456',
        status_ids: [2],
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
      },
    ],
    card_fields: [{ field_identifier: 'title', field_type: 'system', display_order: 0, width: 4 }],
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
  mocks.patchBoardConfiguration.mockImplementation((_collectionId, payload) =>
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

    // A collection's nav has no test entries, so its config page must not
    // offer them either. Workspace-only tools cannot be toggled at collection
    // scope, so the screen must not offer them at all — and neither the
    // workspace-scoped queue view nor the fixed workspace affordances.
    expect(screen.queryByTestId('nav-config-row-test-cases')).not.toBeInTheDocument();
    expect(screen.queryByTestId('nav-config-section-tests')).not.toBeInTheDocument();
    expect(screen.queryByTestId('nav-config-section-tools')).not.toBeInTheDocument();
    for (const id of [...WORKSPACE_ONLY_IDS, QUEUE_ID]) {
      expect(screen.queryByTestId(`nav-config-row-${id}`)).not.toBeInTheDocument();
    }
  });

  test('toggling a view patches only view_settings, never the board columns', async () => {
    mocks.getBoardConfiguration.mockResolvedValue(collectionConfigResponse());
    mocks.getCollection.mockResolvedValue({ id: 7, name: 'Alpha' });

    render(NavigationConfigPage, { props: { workspaceId: 1, collectionId: 7 } });
    await waitFor(() => expect(boardConfigRow('board')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('nav-config-toggle-list'));

    await waitFor(() => expect(mocks.patchBoardConfiguration).toHaveBeenCalledTimes(1));
    const [, payload] = mocks.patchBoardConfiguration.mock.calls[0];
    // The page owns view_settings only. Sending the fetched columns (with
    // board_configuration_id/created_at/updated_at) is what the strict v2
    // decoder rejected as "not valid JSON".
    expect(Object.keys(payload)).toEqual(['view_settings']);
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
    mocks.patchBoardConfiguration
      .mockRejectedValueOnce(new Error('offline'))
      .mockImplementation((_collectionId, payload) =>
        Promise.resolve({ ...collectionConfigResponse(), ...payload })
      );

    render(NavigationConfigPage, { props: { workspaceId: 1, collectionId: 7 } });
    await waitFor(() => expect(boardConfigRow('board')).toBeInTheDocument());

    // Toggle two views in quick succession; the first save fails.
    fireEvent.click(screen.getByTestId('nav-config-toggle-list'));
    fireEvent.click(screen.getByTestId('nav-config-toggle-tree'));

    await waitFor(() => expect(mocks.patchBoardConfiguration).toHaveBeenCalledTimes(2));
    const [, secondPayload] = mocks.patchBoardConfiguration.mock.calls[1];
    // The later toggle must survive the earlier failure and be persisted.
    expect(secondPayload.view_settings.enabled_views).not.toContain('list');
    expect(secondPayload.view_settings.enabled_views).not.toContain('tree');
  });

  test('resetting patches only a null enabled_views override', async () => {
    mocks.getBoardConfiguration.mockResolvedValue(collectionConfigResponse());
    mocks.getCollection.mockResolvedValue({ id: 7, name: 'Alpha' });

    render(NavigationConfigPage, { props: { workspaceId: 1, collectionId: 7 } });
    await waitFor(() => expect(boardConfigRow('board')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('nav-config-reset'));

    await waitFor(() => expect(mocks.patchBoardConfiguration).toHaveBeenCalledTimes(1));
    const [, payload] = mocks.patchBoardConfiguration.mock.calls[0];
    // A full-replace payload here would zero the collection's columns, card
    // fields, and roadmap config.
    expect(Object.keys(payload)).toEqual(['view_settings']);
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
