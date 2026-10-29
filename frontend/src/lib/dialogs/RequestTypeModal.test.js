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
  get: vi.fn(),
}));

vi.mock('../api/channels.js', () => ({ requestTypes: apiMocks }));
vi.mock('../api/workspaces.js', async (importOriginal) => {
  const actual = await importOriginal();
  return {
    ...actual,
    workspaces: { ...actual.workspaces, get: storeMocks.get },
  };
});

vi.mock('../stores/workspaces.svelte.js', () => ({
  workspacesStore: storeMocks,
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key, fallback) => fallback ?? key,
}));

import RequestTypeModal from './RequestTypeModal.svelte';
import { workspacesStore } from '../api/channels.js';

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

  it('resolves configured channel workspaces beyond the cached directory page', async () => {
    // The channel allows one workspace on page one and one beyond it.
    storeMocks.get.mockImplementation(async (id) => workspace(id, `Configured ${id}`));

    render(RequestTypeModal, {
      isOpen: true,
      mode: 'create',
      channelWorkspaceIds: [2, 305],
    });

    // Workspace 2 is already cached; 305 lives past the directory page and
    // must be resolved explicitly.
    await waitFor(() => {
      expect(storeMocks.get).toHaveBeenCalledWith(305);
    });
    expect(storeMocks.get).not.toHaveBeenCalledWith(2);

    await openWorkspacePicker();
    await waitFor(() => {
      expect(document.querySelector('[data-option-value="305"]')).toBeInTheDocument();
    });
    expect(document.querySelector('[data-option-value="305"]')?.textContent).toContain('Configured 305');
    expect(document.querySelector('[data-option-value="2"]')?.textContent).toContain('Workspace 2');
    // The picker shows exactly the configured set, not the whole page.
    expect(document.querySelector('[data-option-value="1"]')).not.toBeInTheDocument();
  });

  it('keeps an existing selection visible when it lives beyond the cached page', async () => {
    storeMocks.get.mockImplementation(async (id) => workspace(id, `Selected ${id}`));
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
      expect(storeMocks.get).toHaveBeenCalledWith(333);
    });

    await openWorkspacePicker();
    await waitFor(() => {
      expect(document.querySelector('[data-option-value="333"]')).toBeInTheDocument();
    });
    expect(document.querySelector('[data-option-value="333"]')?.textContent).toContain('Selected 333');
  });

  it('falls back to the cached page and server search for unrestricted channels', async () => {
    render(RequestTypeModal, { isOpen: true, mode: 'create' });

    await openWorkspacePicker();
    // The cached page renders without any per-id fetch.
    await waitFor(() => {
      expect(document.querySelector('[data-option-value="1"]')).toBeInTheDocument();
    });
    expect(storeMocks.get).not.toHaveBeenCalled();
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
