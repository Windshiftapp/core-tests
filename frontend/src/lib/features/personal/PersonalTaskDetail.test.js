import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api.js', () => ({
  api: {
    items: {
      get: vi.fn(),
      update: vi.fn(),
    },
    hierarchyLevels: {
      getAll: vi.fn(),
    },
  },
}));

vi.mock('../../stores', async () => {
  const { writable } = await import('svelte/store');
  return {
    aiStore: { available: false },
    attachmentStatus: { enabled: false },
    workspacesStore: writable({ personalWorkspace: { id: 7 } }),
  };
});

vi.mock('../../stores/workspaceDataStore.svelte.js', () => ({
  workspaceDataStore: {
    initialize: vi.fn().mockResolvedValue(undefined),
    workspace: { id: 7, key: 'ME' },
    statuses: [],
    itemTypes: [],
  },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: vi.fn((key) => key),
}));

vi.mock('../items/Comments.svelte', () => ({
  default: function MockComments() {},
}));

vi.mock('../items/ItemDetailBreadcrumbs.svelte', () => ({
  default: function MockItemDetailBreadcrumbs() {},
}));

vi.mock('../../editors/LazyMilkdownEditor.svelte', async () => ({
  default: (await import('./PersonalTaskEditorStub.svelte')).default,
}));

vi.mock('../assets/AttachmentDiagramList.svelte', () => ({
  default: function MockAttachmentDiagramList() {},
}));

vi.mock('runed', () => ({
  onClickOutside: vi.fn(),
  useEventListener: vi.fn(),
}));

import { api } from '../../api.js';
import PersonalTaskDetail from './PersonalTaskDetail.svelte';

describe('PersonalTaskDetail description editing', () => {
  beforeEach(() => {
    api.items.get.mockResolvedValue({
      id: 42,
      title: 'Pack rucksack for England',
      description: '',
      status_id: 1,
      workspace_item_number: 12,
    });
    api.hierarchyLevels.getAll.mockResolvedValue([]);
  });

  it('opens the description editor when the empty description is clicked', async () => {
    render(PersonalTaskDetail, {
      props: {
        itemId: 42,
        workspaceId: 7,
        statuses: [{ id: 1, name: 'Open', category_name: 'To Do' }],
        isModal: false,
      },
    });

    const emptyDescription = await screen.findByTestId('item-description-empty');
    await fireEvent.click(emptyDescription);

    await waitFor(() => {
      expect(screen.getByTestId('item-description-editor')).toBeInTheDocument();
    });
  });
});
