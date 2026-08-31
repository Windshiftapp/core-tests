import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeAll, beforeEach, describe, expect, test, vi } from 'vitest';

const mocks = vi.hoisted(() => {
  const getAll = vi.fn();
  const create = vi.fn();
  const canAdminWorkspace = vi.fn();
  const hasPermission = vi.fn();
  const listeners = new Set();
  let permissionValue = { userPermissionKeys: new Set() };
  const permissionStore = {
    subscribe(run) {
      listeners.add(run);
      run(permissionValue);
      return () => listeners.delete(run);
    },
    set(value) {
      permissionValue = value;
      listeners.forEach((run) => run(permissionValue));
    },
  };

  return {
    getAll,
    create,
    canAdminWorkspace,
    hasPermission,
    permissionStore,
    isSystemAdmin: {
      subscribe: (run) => {
        run(false);
        return () => {};
      },
    },
  };
});

vi.mock('../api.js', () => ({
  api: {
    milestones: { getAll: mocks.getAll, create: mocks.create },
    milestoneCategories: {},
  },
}));

vi.mock('../stores/permissions.svelte.js', () => ({
  permissionStore: mocks.permissionStore,
  isSystemAdmin: mocks.isSystemAdmin,
}));

vi.mock('../stores/workspacePermissions.svelte.js', () => ({
  workspacePermissions: {
    canAdminWorkspace: mocks.canAdminWorkspace,
    hasPermission: mocks.hasPermission,
  },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key, params = {}) => {
    if (key === 'pickers.noResultsFor') return `No results for "${params.query}"`;
    if (key === 'pickers.createItem') return `Create "${params.value}"`;
    if (key === 'pickers.search') return 'Search';
    return key;
  },
}));

vi.mock('../stores/toasts.svelte.js', () => ({
  errorToast: vi.fn(),
}));

import { milestonesStore } from '../stores/milestones.js';
import MilestoneCombobox from './MilestoneCombobox.svelte';

const { canAdminWorkspace, create, getAll, hasPermission, permissionStore } = mocks;

beforeAll(() => {
  if (!Element.prototype.animate) {
    Element.prototype.animate = () => ({
      finished: Promise.resolve(),
      cancel: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      play: () => {},
      pause: () => {},
    });
  }
  if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {};
  if (!globalThis.ResizeObserver) {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  }
});

beforeEach(() => {
  getAll.mockReset();
  create.mockReset();
  permissionStore.set({ userPermissionKeys: new Set() });
  canAdminWorkspace.mockReset().mockReturnValue(false);
  hasPermission.mockReset().mockReturnValue(false);
  milestonesStore.reset();
});

afterEach(() => {
  cleanup();
  document.body.innerHTML = '';
});

async function openCreateAction({ workspaceId = 23, onSelect = vi.fn() } = {}) {
  render(MilestoneCombobox, {
    props: {
      multiple: true,
      workspaceId,
      milestones: [],
      value: [],
      onSelect,
    },
  });

  await fireEvent.click(screen.getByRole('combobox'));
  await fireEvent.input(screen.getByPlaceholderText('Search'), {
    target: { value: '0.8.7' },
  });
  return { onSelect };
}

describe('MilestoneCombobox creation', () => {
  test('opens the native dialog with the typed name and creates a workspace milestone', async () => {
    hasPermission.mockImplementation(
      (workspaceId, permission) => workspaceId === 23 && permission === 'item.edit'
    );
    const createdMilestone = { id: 91, name: '0.8.7', is_global: false, workspace_id: 23 };
    create.mockResolvedValue(createdMilestone);
    const { onSelect } = await openCreateAction();

    await fireEvent.click(await screen.findByTestId('milestone-create-option'));

    const dialog = await screen.findByTestId('milestone-form-dialog');
    expect(dialog.querySelector('#milestone-name')).toHaveValue('0.8.7');

    await fireEvent.click(screen.getByTestId('dialog-confirm'));

    await waitFor(() => {
      expect(create).toHaveBeenCalledWith({
        name: '0.8.7',
        description: '',
        target_date: null,
        status: 'planning',
        category_id: null,
        is_global: false,
        workspace_id: 23,
      });
    });
    expect(onSelect).toHaveBeenCalledWith({
      ids: [91],
      milestones: [createdMilestone],
    });
  });

  test('uses global scope when only global milestone permission is available', async () => {
    permissionStore.set({ userPermissionKeys: new Set(['milestone.create']) });
    const createdMilestone = { id: 92, name: 'global-0.8.7', is_global: true, workspace_id: null };
    create.mockResolvedValue(createdMilestone);

    await openCreateAction();
    await fireEvent.input(screen.getByPlaceholderText('Search'), {
      target: { value: 'global-0.8.7' },
    });
    await fireEvent.click(await screen.findByTestId('milestone-create-option'));

    const dialog = await screen.findByTestId('milestone-form-dialog');
    expect(dialog.querySelector('#milestone-name')).toHaveValue('global-0.8.7');
    expect(dialog.querySelector('button[data-testid="dialog-confirm"]')).toBeInTheDocument();
    expect(screen.queryByText('milestones.switchTo')).not.toBeInTheDocument();

    await fireEvent.click(screen.getByTestId('dialog-confirm'));

    await waitFor(() => {
      expect(create).toHaveBeenCalledWith(
        expect.objectContaining({
          is_global: true,
          workspace_id: null,
        })
      );
    });
  });

  test('does not expose creation without workspace or global permission', async () => {
    await openCreateAction();

    expect(screen.queryByTestId('milestone-create-option')).not.toBeInTheDocument();
    expect(screen.queryByTestId('milestone-form-dialog')).not.toBeInTheDocument();
  });
});
