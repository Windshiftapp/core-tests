import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMocks = vi.hoisted(() => ({
  get: vi.fn(),
  create: vi.fn(),
  update: vi.fn(),
  getAvailableItemTypes: vi.fn(),
}));

const storeMocks = vi.hoisted(() => ({
  load: vi.fn(),
  searchWorkspaces: vi.fn(),
  getByIds: vi.fn(),
}));

vi.mock('../api/channels.js', () => ({ requestTypes: apiMocks }));
vi.mock('../api/workspaces.js', async (importOriginal) => {
  const actual = await importOriginal();
  return {
    ...actual,
    workspaces: { ...actual.workspaces, getByIds: storeMocks.getByIds },
  };
});

vi.mock('../stores/workspaces.svelte.js', () => ({
  workspacesStore: storeMocks,
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key, fallback) => fallback ?? key,
}));

import RequestTypeModal from './RequestTypeModal.svelte';
import { FOREGROUND_READ } from '../api/readScheduler.js';

function workspace(id, name) {
  return { id, name, key: `WS${id}` };
}

// 210 cached workspaces: the directory's first server page.
function cachedDirectory() {
  return Array.from({ length: 210 }, (_, i) => workspace(i + 1, `Workspace ${i + 1}`));
}

async function openWorkspacePicker() {
  const input = screen.getByPlaceholderText('Select workspace');
  await fireEvent.click(input);
}

describe('RequestTypeModal workspace choices', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.getAvailableItemTypes.mockResolvedValue([]);
    apiMocks.create.mockResolvedValue({});
    apiMocks.update.mockResolvedValue({});
    storeMocks.load.mockResolvedValue(cachedDirectory());
    storeMocks.searchWorkspaces.mockResolvedValue({ workspaces: [], total: 0 });
  });

  it('resolves configured channel workspaces beyond the cached page in one read', async () => {
    // The channel allows one workspace on page one and one beyond it.
    storeMocks.getByIds.mockResolvedValue([workspace(305, 'Configured 305')]);

    render(RequestTypeModal, {
      isOpen: true,
      mode: 'create',
      channelWorkspaceIds: [2, 305],
    });

    // Workspace 2 is already cached; 305 lives past the directory page and
    // must be resolved — with one batch read, not one read per missing id.
    await waitFor(() => {
      expect(storeMocks.getByIds).toHaveBeenCalledTimes(1);
    });
    expect(storeMocks.getByIds).toHaveBeenCalledWith([305], FOREGROUND_READ);

    await openWorkspacePicker();
    await waitFor(() => {
      expect(document.querySelector('[data-option-value="305"]')).toBeInTheDocument();
    });
    expect(document.querySelector('[data-option-value="305"]')?.textContent).toContain('Configured 305');
    expect(document.querySelector('[data-option-value="2"]')?.textContent).toContain('Workspace 2');
    // The picker shows exactly the configured set, not the whole page.
    expect(document.querySelector('[data-option-value="1"]')).not.toBeInTheDocument();
  });

  it('resolves a large configured set with one bounded request', async () => {
    const configured = Array.from({ length: 180 }, (_, i) => 1000 + i);
    storeMocks.getByIds.mockResolvedValue(configured.map((id) => workspace(id, `Configured ${id}`)));

    render(RequestTypeModal, {
      isOpen: true,
      mode: 'create',
      channelWorkspaceIds: configured,
    });

    await waitFor(() => {
      expect(storeMocks.getByIds).toHaveBeenCalledTimes(1);
    });
    // All 180 configured ids were resolved by the single batch read.
    expect(storeMocks.getByIds).toHaveBeenCalledWith(configured, FOREGROUND_READ);
  });

  it('keeps an existing selection visible when it lives beyond the cached page', async () => {
    storeMocks.getByIds.mockResolvedValue([workspace(333, 'Selected 333')]);
    const requestType = {
      id: 7,
      name: 'Existing type',
      workspace_id: 333,
      item_type_id: null,
    };
    apiMocks.get.mockResolvedValue({ ...requestType, title_template: '' });

    render(RequestTypeModal, {
      isOpen: true,
      mode: 'edit',
      requestType,
      channelWorkspaceIds: [333],
    });

    await waitFor(() => {
      expect(storeMocks.getByIds).toHaveBeenCalledWith([333], FOREGROUND_READ);
    });

    await openWorkspacePicker();
    await waitFor(() => {
      expect(document.querySelector('[data-option-value="333"]')).toBeInTheDocument();
    });
    expect(document.querySelector('[data-option-value="333"]')?.textContent).toContain('Selected 333');
  });

  it('reports an unresolvable id without dropping the resolvable ones', async () => {
    // The server omits ids the caller cannot view.
    storeMocks.getByIds.mockResolvedValue([]);
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    render(RequestTypeModal, {
      isOpen: true,
      mode: 'create',
      channelWorkspaceIds: [2, 999],
    });

    await waitFor(() => {
      expect(errorSpy).toHaveBeenCalledWith('Failed to resolve configured workspaces:', '999');
    });

    await openWorkspacePicker();
    await waitFor(() => {
      expect(document.querySelector('[data-option-value="2"]')).toBeInTheDocument();
    });
    expect(document.querySelector('[data-option-value="999"]')).not.toBeInTheDocument();
  });

  it('falls back to the cached page and server search for unrestricted channels', async () => {
    render(RequestTypeModal, { isOpen: true, mode: 'create' });

    await openWorkspacePicker();
    // The cached page renders without any batch resolve.
    await waitFor(() => {
      expect(document.querySelector('[data-option-value="1"]')).toBeInTheDocument();
    });
    expect(storeMocks.getByIds).not.toHaveBeenCalled();
  });

  it('omits is_active on edit so a disabled request type stays disabled', async () => {
    const requestType = {
      id: 7,
      name: 'Existing type',
      workspace_id: 2,
      item_type_id: 3,
      is_active: false,
      title_template: 'Ticket {{id}}',
    };
    apiMocks.get.mockResolvedValue({ ...requestType });

    render(RequestTypeModal, {
      isOpen: true,
      mode: 'edit',
      requestType,
      channelWorkspaceIds: [2],
    });

    await waitFor(() => expect(apiMocks.get).toHaveBeenCalledWith(7));
    await fireEvent.click(screen.getByTestId('dialog-confirm'));

    await waitFor(() => expect(apiMocks.update).toHaveBeenCalledTimes(1));
    const payload = apiMocks.update.mock.calls[0][2];
    expect(payload).not.toHaveProperty('is_active');
    expect(payload).not.toHaveProperty('display_order');
    expect(payload.title_template).toBe('Ticket {{id}}');
  });
});
