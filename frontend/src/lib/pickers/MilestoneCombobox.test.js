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

vi.mock('../api/milestones.js', () => ({
  milestones: { getAll: mocks.getAll, create: mocks.create },
  milestoneCategories: {},
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
import { milestoneCategories, milestones } from '../api/milestones.js';

const { canAdminWorkspace, create, getAll, hasPermission, permissionStore } = mocks;

beforeAll(() => {
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
  test('cancels an internal milestone load when unmounted', async () => {
    getAll.mockResolvedValue([]);

    const view = render(MilestoneCombobox, {
      props: {
        workspaceId: 23,
      },
    });

    await waitFor(() => expect(getAll).toHaveBeenCalledOnce());
    const requestOptions = getAll.mock.calls[0][1];
    expect(requestOptions.signal).toBeInstanceOf(AbortSignal);
    expect(requestOptions.signal.aborted).toBe(false);

    view.unmount();

    expect(requestOptions.signal.aborted).toBe(true);
  });

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

  test('notifies the caller after an inline create so cached options can refresh', async () => {
    hasPermission.mockImplementation(
      (workspaceId, permission) => workspaceId === 23 && permission === 'item.edit'
    );
    const createdMilestone = { id: 93, name: '0.9.0', is_global: false, workspace_id: 23 };
    create.mockResolvedValue(createdMilestone);
    const onCreated = vi.fn();

    render(MilestoneCombobox, {
      props: {
        multiple: true,
        workspaceId: 23,
        milestones: [],
        value: [],
        onSelect: vi.fn(),
        onCreated
      }
    });
    await fireEvent.click(screen.getByRole('combobox'));
    await fireEvent.input(screen.getByPlaceholderText('Search'), {
      target: { value: '0.9.0' }
    });
    await fireEvent.click(await screen.findByTestId('milestone-create-option'));
    await fireEvent.click(await screen.findByTestId('dialog-confirm'));

    await waitFor(() => expect(onCreated).toHaveBeenCalledWith(createdMilestone));
  });

  test('renders the inline create dialog above the create-modal tier', async () => {
    hasPermission.mockImplementation(
      (workspaceId, permission) => workspaceId === 23 && permission === 'item.edit'
    );
    await openCreateAction();

    await fireEvent.click(await screen.findByTestId('milestone-create-option'));

    const dialog = await screen.findByTestId('milestone-form-dialog');
    expect(dialog.className).toContain('z-[70]');
  });

  test('does not expose creation without workspace or global permission', async () => {
    await openCreateAction();

    expect(screen.queryByTestId('milestone-create-option')).not.toBeInTheDocument();
    expect(screen.queryByTestId('milestone-form-dialog')).not.toBeInTheDocument();
  });
});

// A saved milestone id missing from the (lazily loaded) options must stay
// visible in the multi-select trigger. MilestoneCombobox has to declare and
// forward resolveMissingLabel — Svelte silently drops unknown props, so an
// undeclared prop would strand the value without a label (and ItemPicker
// would hide the chip entirely).
describe('MilestoneCombobox — resolveMissingLabel forwarding', () => {
  test('labels a selected value that is not in the loaded milestones', async () => {
    render(MilestoneCombobox, {
      props: {
        multiple: true,
        workspaceId: 23,
        milestones: [{ id: 5, name: 'Sprint 5' }],
        value: [5, 42],
        resolveMissingLabel: (v) => (v === 42 ? 'Milestone #42' : null),
        onSelect: vi.fn(),
      },
    });

    expect(screen.getByText('Sprint 5')).toBeInTheDocument();
    expect(screen.getByText('Milestone #42')).toBeInTheDocument();
  });

  test('an unlabeled missing value falls back to the placeholder instead of a bare chip', async () => {
    render(MilestoneCombobox, {
      props: {
        multiple: true,
        workspaceId: 23,
        milestones: [{ id: 5, name: 'Sprint 5' }],
        value: [5],
        resolveMissingLabel: () => null,
        onSelect: vi.fn(),
      },
    });

    expect(screen.getByText('Sprint 5')).toBeInTheDocument();
  });
});

describe('MilestoneCombobox configuration options', () => {
  test('allowCreate=false suppresses creation even when the user may create milestones', async () => {
    permissionStore.set({ userPermissionKeys: new Set(['milestone.create']) });
    render(MilestoneCombobox, {
      props: {
        multiple: true,
        workspaceId: null,
        milestones: [{ id: 1, name: 'Global A', is_global: true }],
        value: [],
        allowCreate: false,
        onSelect: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('combobox'));
    await fireEvent.input(screen.getByPlaceholderText('Search'), {
      target: { value: 'brand new milestone' },
    });

    expect(screen.queryByTestId('milestone-create-option')).not.toBeInTheDocument();
  });

  test('includeCompleted lists terminal milestones and drops the footer toggle', async () => {
    render(MilestoneCombobox, {
      props: {
        multiple: true,
        workspaceId: null,
        milestones: [
          { id: 1, name: 'Active', status: 'planning', is_global: true },
          { id: 2, name: 'Done', status: 'completed', is_global: true },
        ],
        value: [],
        includeCompleted: true,
        onSelect: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('combobox'));

    expect(screen.getByText('Active')).toBeInTheDocument();
    expect(screen.getByText('Done')).toBeInTheDocument();
    expect(screen.queryByTestId('milestone-show-completed-toggle')).not.toBeInTheDocument();
  });

  test('terminal milestones stay hidden by default until the footer toggle is used', async () => {
    render(MilestoneCombobox, {
      props: {
        multiple: true,
        workspaceId: null,
        milestones: [
          { id: 1, name: 'Active', status: 'planning', is_global: true },
          { id: 2, name: 'Done', status: 'completed', is_global: true },
        ],
        value: [],
        onSelect: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('combobox'));

    expect(screen.getByText('Active')).toBeInTheDocument();
    expect(screen.queryByText('Done')).not.toBeInTheDocument();
    expect(screen.getByTestId('milestone-show-completed-toggle')).toBeInTheDocument();
  });
});

describe('MilestoneCombobox provided options', () => {
  test('does not issue an internal mount request when options are provided', async () => {
    render(MilestoneCombobox, {
      props: {
        multiple: true,
        workspaceId: 23,
        milestones: [{ id: 5, name: 'Sprint 5' }],
        value: [],
        onSelect: vi.fn(),
      },
    });

    await Promise.resolve();
    expect(getAll).not.toHaveBeenCalled();
  });

  test('calls onOpen when the picker opens instead of loading on mount', async () => {
    const onOpen = vi.fn();
    render(MilestoneCombobox, {
      props: {
        multiple: true,
        workspaceId: 23,
        milestones: [],
        value: [],
        onOpen,
        onSelect: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByRole('combobox'));

    expect(onOpen).toHaveBeenCalledTimes(1);
    expect(getAll).not.toHaveBeenCalled();
  });

  test('treats a provided empty array as authoritative', async () => {
    render(MilestoneCombobox, {
      props: {
        multiple: true,
        workspaceId: 23,
        milestones: [],
        value: [],
        onSelect: vi.fn(),
      },
    });

    await Promise.resolve();
    expect(getAll).not.toHaveBeenCalled();
  });
});
