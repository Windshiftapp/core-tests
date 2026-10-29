import { fireEvent, render, waitFor } from '@testing-library/svelte';
import { writable } from 'svelte/store';
import { afterEach, describe, expect, test, vi } from 'vitest';

const reload = vi.hoisted(() => vi.fn().mockResolvedValue(undefined));
const createWorkspace = vi.hoisted(() => vi.fn());

vi.mock('../api/workspaces.js', () => ({
  workspaces: {
        create: createWorkspace,
        getTemplates: vi.fn().mockResolvedValue([]),
      },
}))

vi.mock('../api/items.js', () => ({
  items: {},
}))

vi.mock('../api/collections.js', () => ({
  collections: {},
  collectionCategories: { getAll: vi.fn().mockResolvedValue([]) },
}))

vi.mock('../api/milestones.js', () => ({
  milestones: {},
  milestoneCategories: { getAll: vi.fn().mockResolvedValue([]) },
}))

vi.mock('../api/misc.js', () => ({
  attachments: {},
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
  workItemFormStore.formData = { name: '', workspace_id: null };
  workItemFormStore.init = vi.fn();
  workItemFormStore.resetForm = vi.fn();
  workItemFormStore.validate = vi.fn(() => false);
  return { workItemFormStore };
});

vi.mock('../stores/permissions.svelte.js', () => ({
  permissionStore: writable({ userPermissionKeys: new Set(['workspace.create']) }),
  isSystemAdmin: writable(false),
}))

vi.mock('../stores/workspacePermissions.svelte.js', () => ({
  workspacePermissions: { reload },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('../stores/toasts.svelte.js', () => ({
  errorToast: vi.fn(),
}));

vi.mock('../utils/createdItemToast.js', () => ({
  showCreatedItemToast: vi.fn(),
}));

import CreateModal from './CreateModal.svelte';
import { workspaces } from '../api/workspaces.js';
import { items } from '../api/items.js';
import { collections } from '../api/collections.js';
import { milestones } from '../api/milestones.js';
import { attachments } from '../api/misc.js';





afterEach(() => {
  document.body.innerHTML = '';
  vi.clearAllMocks();
});

describe('CreateModal workspace creation', () => {
  test('refreshes workspace permissions after creating a workspace', async () => {
    createWorkspace.mockResolvedValue({ id: 3, name: 'Platform', key: 'PLAT' });

    render(CreateModal, {
      props: { isOpen: true, initialType: 'workspace', skipNavigate: true },
    });

    fireEvent.input(document.querySelector('#workspace-name'), {
      target: { value: 'Platform' },
    });
    fireEvent.input(document.querySelector('#workspace-key'), {
      target: { value: 'PLAT' },
    });

    fireEvent.click(document.querySelector('#create-modal-submit'));

    await waitFor(() => {
      expect(createWorkspace).toHaveBeenCalledTimes(1);
    });
    await waitFor(() => {
      expect(reload).toHaveBeenCalledTimes(1);
    });
  });
});
