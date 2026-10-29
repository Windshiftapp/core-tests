import { fireEvent, render, waitFor } from '@testing-library/svelte';
import { writable } from 'svelte/store';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

const createItem = vi.hoisted(() => vi.fn());
const updateItem = vi.hoisted(() => vi.fn());
const uploadToItem = vi.hoisted(() => vi.fn());
const getContentUrl = vi.hoisted(() => vi.fn());
const errorToast = vi.hoisted(() => vi.fn());
const showCreatedItemToast = vi.hoisted(() => vi.fn());
const oncreated = vi.hoisted(() => vi.fn());
const formData = vi.hoisted(() => ({
  name: 'New item',
  workspace_id: 1,
  item_type_id: 2,
  description: 'before blob:temp after',
  parent_id: null,
}));
const pendingDescriptionImages = vi.hoisted(() => [
  { url: 'blob:temp', file: { name: 'screenshot.png' } },
]);

vi.mock('../api/items.js', () => ({
  items: { create: createItem, update: updateItem },
}))

vi.mock('../api/misc.js', () => ({
  attachments: { uploadToItem, getContentUrl },
}))

vi.mock('../api/workspaces.js', () => ({
  workspaces: { create: vi.fn(), getTemplates: vi.fn().mockResolvedValue([]) },
}))

vi.mock('../api/collections.js', () => ({
  collections: {},
  collectionCategories: { getAll: vi.fn().mockResolvedValue([]) },
}))

vi.mock('../api/milestones.js', () => ({
  milestones: {},
  milestoneCategories: { getAll: vi.fn().mockResolvedValue([]) },
}));

vi.mock('../router.js', () => ({
  navigate: vi.fn(),
  currentRoute: writable({ view: 'workspaces', params: {} }),
}));

vi.mock('../stores/workspaces.svelte.js', () => ({
  workspacesStore: Object.assign(writable({ loaded: true, regularWorkspaces: [] }), {
      add: vi.fn(),
      load: vi.fn(),
    }),
}))

vi.mock('../stores/navigationConfig.svelte.js', () => ({
  shouldNavigateAfterCreate: vi.fn(() => false),
}))

vi.mock('../stores/workItemFormStore.svelte.js', async () => {
  // Keep the real store for rendering the form's configured fields, but
  // drive the create flow from the form data this test controls.
  const { workItemFormStore } = await vi.importActual(
    '../stores/workItemFormStore.svelte.js'
  );
  workItemFormStore.formData = formData;
  workItemFormStore.pendingDescriptionImages = pendingDescriptionImages;
  workItemFormStore.init = vi.fn();
  workItemFormStore.resetForm = vi.fn();
  validate: vi.fn(() => true),
  workItemFormStore.getFormData = vi.fn(() => formData);
  return { workItemFormStore };
});

vi.mock('../stores/permissions.svelte.js', () => ({
  permissionStore: writable({ userPermissionKeys: new Set() }),
  isSystemAdmin: writable(false),
}))

vi.mock('../stores/workspacePermissions.svelte.js', () => ({
  workspacePermissions: {
      canAdminWorkspace: vi.fn(() => false),
      hasPermission: vi.fn(() => false),
      reload: vi.fn(),
    },
}));

vi.mock('../stores/i18n.svelte.js', () => ({ t: (key) => key }));
vi.mock('../stores/toasts.svelte.js', () => ({ errorToast }));
vi.mock('../utils/createdItemToast.js', () => ({ showCreatedItemToast }));

import CreateModal from './CreateModal.svelte';
import { items } from '../api/items.js';
import { attachments } from '../api/misc.js';
import { workspaces } from '../api/workspaces.js';
import { collections } from '../api/collections.js';
import { milestones } from '../api/milestones.js';





beforeEach(() => {
  formData.description = 'before blob:temp after';
  pendingDescriptionImages.splice(0, pendingDescriptionImages.length, {
    url: 'blob:temp',
    file: { name: 'screenshot.png' },
  });
  createItem.mockReset().mockResolvedValue({ id: 101, name: 'New item' });
  updateItem.mockReset().mockImplementation((id, data) => Promise.resolve({ id, ...data }));
  uploadToItem.mockReset();
  getContentUrl.mockReset().mockReturnValue('/api/v2/attachments/55/content');
  errorToast.mockReset();
  showCreatedItemToast.mockReset();
  oncreated.mockReset();
});

afterEach(() => {
  document.body.innerHTML = '';
  vi.clearAllMocks();
});

async function submit() {
  await fireEvent.click(document.querySelector('#create-modal-submit'));
}

describe('CreateModal committed-item retry', () => {
  test('a failed image upload retries against the created item without a duplicate', async () => {
    uploadToItem.mockRejectedValueOnce(new Error('upload offline')).mockResolvedValueOnce({ id: 55 });

    render(CreateModal, { props: { isOpen: true, initialType: 'work-item', oncreated } });

    await submit();
    await waitFor(() => expect(errorToast).toHaveBeenCalledTimes(1));
    expect(createItem).toHaveBeenCalledTimes(1);
    expect(oncreated).not.toHaveBeenCalled();

    await submit();
    await waitFor(() => expect(oncreated).toHaveBeenCalledTimes(1));

    expect(createItem).toHaveBeenCalledTimes(1);
    expect(updateItem).toHaveBeenCalledTimes(1);
    expect(updateItem).toHaveBeenCalledWith(101, {
      description: 'before /api/v2/attachments/55/content after',
    });
    expect(showCreatedItemToast).toHaveBeenCalledTimes(1);
  });

  test('a failed description patch retries against the created item without a duplicate', async () => {
    uploadToItem.mockResolvedValue({ id: 55 });
    updateItem.mockRejectedValueOnce(new Error('patch offline')).mockResolvedValue({ id: 101 });

    render(CreateModal, { props: { isOpen: true, initialType: 'work-item', oncreated } });

    await submit();
    await waitFor(() => expect(errorToast).toHaveBeenCalledTimes(1));
    expect(createItem).toHaveBeenCalledTimes(1);
    expect(oncreated).not.toHaveBeenCalled();

    await submit();
    await waitFor(() => expect(oncreated).toHaveBeenCalledTimes(1));

    expect(createItem).toHaveBeenCalledTimes(1);
    expect(updateItem).toHaveBeenCalledTimes(2);
    expect(showCreatedItemToast).toHaveBeenCalledTimes(1);
  });

  test('a normal create uploads once and reports success', async () => {
    uploadToItem.mockResolvedValue({ id: 55 });

    render(CreateModal, { props: { isOpen: true, initialType: 'work-item', oncreated } });

    await submit();
    await waitFor(() => expect(oncreated).toHaveBeenCalledTimes(1));

    expect(createItem).toHaveBeenCalledTimes(1);
    expect(updateItem).toHaveBeenCalledTimes(1);
    expect(errorToast).not.toHaveBeenCalled();
  });
});
