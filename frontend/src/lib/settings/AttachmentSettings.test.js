import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api/misc.js', () => ({
  attachmentSettings: {
        get: vi.fn(),
        getStatus: vi.fn(),
        update: vi.fn(),
      },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('../stores/toasts.svelte.js', () => ({
  successToast: vi.fn(),
}));

import { attachmentSettings } from '../api/misc.js';
import { attachmentStatus } from '../stores/attachmentStatus.svelte.js';
import AttachmentSettings from './AttachmentSettings.svelte';

const disabledSettings = {
  id: 1,
  max_file_size: 52428800,
  allowed_mime_types: '[]',
  attachment_path: '/data/attachments',
  enabled: false,
};

const enabledSettings = {
  ...disabledSettings,
  enabled: true,
};

describe('AttachmentSettings availability synchronization', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    attachmentStatus.hydrate({ enabled: false, writable: true });
    attachmentSettings.get
      .mockResolvedValueOnce(disabledSettings)
      .mockResolvedValue(enabledSettings);
    attachmentSettings.getStatus
      .mockResolvedValueOnce({
        enabled: false,
        attachment_path: '/data/attachments',
        writable: true,
      })
      .mockResolvedValue({
        enabled: true,
        attachment_path: '/data/attachments',
        writable: true,
      });
    attachmentSettings.update.mockResolvedValue(enabledSettings);
  });

  it('refreshes shared availability after attachments are enabled', async () => {
    render(AttachmentSettings);

    const toggle = await screen.findByRole('switch');
    expect(toggle).toHaveAttribute('aria-checked', 'false');
    expect(attachmentStatus.enabled).toBe(false);

    await fireEvent.click(toggle);

    await waitFor(() => {
      expect(attachmentSettings.update).toHaveBeenCalledWith(
        1,
        expect.objectContaining({ enabled: true })
      );
      expect(attachmentSettings.getStatus).toHaveBeenCalledTimes(2);
      expect(attachmentStatus.enabled).toBe(true);
      expect(screen.getByText('settings.attachments.attachmentsEnabled')).toBeInTheDocument();
    });
  });

  it('distinguishes disabled attachments from unwritable storage', () => {
    attachmentStatus.hydrate({ enabled: false, writable: true });
    expect(attachmentStatus.unavailableReason).toBe('disabled');

    attachmentStatus.hydrate({ enabled: true, writable: false });
    expect(attachmentStatus.enabled).toBe(false);
    expect(attachmentStatus.unavailableReason).toBe('unwritable');
  });
});
