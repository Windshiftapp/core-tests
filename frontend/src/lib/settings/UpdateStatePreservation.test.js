import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

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
});

vi.mock('../api.js', () => ({
  api: {
    getUsers: vi.fn(),
    groups: {
      getAll: vi.fn(),
      create: vi.fn(),
      update: vi.fn(),
    },
    notificationSettings: {
      getAll: vi.fn(),
      getAvailableEvents: vi.fn(),
      create: vi.fn(),
      update: vi.fn(),
    },
  },
}));

vi.mock('../stores/toasts.svelte.js', () => ({
  errorToast: vi.fn(),
}));

vi.mock('../composables/useConfirm.js', () => ({
  confirm: vi.fn(),
}));

import { api } from '../api.js';
import GroupManager from './GroupManager.svelte';
import NotificationSettings from './NotificationSettings.svelte';

async function openRowEdit(rowText, editText) {
  const row = (await screen.findByText(rowText)).closest('tr');
  await fireEvent.click(within(row).getByRole('button'));
  await fireEvent.click(await screen.findByText(editText));
}

describe('settings update state preservation', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.getUsers.mockResolvedValue([]);
    api.groups.update.mockResolvedValue({});
    api.notificationSettings.getAvailableEvents.mockResolvedValue([]);
    api.notificationSettings.update.mockResolvedValue({});
  });

  it('keeps an inactive group inactive when editing its details', async () => {
    api.groups.getAll.mockResolvedValue([
      {
        id: 41,
        name: 'Inactive Group',
        description: 'Paused',
        is_active: false,
        member_count: 0,
      },
    ]);

    render(GroupManager);
    await openRowEdit('Inactive Group', 'settings.groups.edit');
    await fireEvent.click(screen.getByTestId('dialog-confirm'));

    await waitFor(() => {
      expect(api.groups.update).toHaveBeenCalledWith(
        41,
        expect.objectContaining({ is_active: false })
      );
    });
  });

  it('keeps an inactive notification setting inactive when editing its details', async () => {
    api.notificationSettings.getAll.mockResolvedValue([
      {
        id: 52,
        name: 'Inactive notifications',
        description: 'Paused',
        is_active: false,
        created_by: 1,
        created_by_name: 'Admin User',
        event_rules: [],
      },
    ]);

    render(NotificationSettings);
    await openRowEdit('Inactive notifications - Paused', 'common.edit');
    await fireEvent.click(screen.getByTestId('dialog-confirm'));

    await waitFor(() => {
      expect(api.notificationSettings.update).toHaveBeenCalledWith(
        52,
        expect.objectContaining({ is_active: false })
      );
    });
  });
});
