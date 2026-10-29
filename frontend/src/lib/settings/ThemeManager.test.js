import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { writable } from 'svelte/store';

vi.mock('../api/admin.js', () => ({
  themes: {
        getAll: vi.fn(),
        getActive: vi.fn(),
        create: vi.fn(),
        update: vi.fn(),
        activate: vi.fn(),
        delete: vi.fn(),
      },
}))

vi.mock('../api/misc.js', () => ({
  attachments: {
        upload: vi.fn(),
      },
}))

vi.mock('../api/objectTranslations.js', () => ({
  objectTranslations: {
        list: vi.fn().mockResolvedValue([]),
        resolve: vi.fn().mockResolvedValue([]),
        upsert: vi.fn().mockResolvedValue(undefined),
        delete: vi.fn().mockResolvedValue(undefined),
      },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  i18n: {
    locale: 'en',
    supportedLocales: [{ code: 'en', name: 'English' }],
  },
  t: (key) => key,
}));

vi.mock('../stores/permissions.svelte.js', () => ({
  isSystemAdmin: writable(true),
}));

import { attachments } from '../api/misc.js';
import { themes } from '../api/admin.js';
import { attachmentStatus } from '../stores/attachmentStatus.svelte.js';
import ThemeManager from './ThemeManager.svelte';


import { objectTranslations } from '../api/objectTranslations.js';


const editableTheme = {
  id: 7,
  name: 'Acme',
  description: 'Company theme',
  is_default: false,
  is_active: true,
  nav_background_color_light: '#ffffff',
  nav_text_color_light: '#374151',
  nav_background_color_dark: '#1f2937',
  nav_text_color_dark: '#f3f4f6',
  logo_url: '',
  logo_url_dark: '',
};

function uploadPayload() {
  const upload = attachments.upload.mock.calls.at(-1)?.[0];
  expect(upload).toBeInstanceOf(FormData);
  return {
    entityType: upload.get('entity_type'),
    file: upload.get('file'),
  };
}

describe('ThemeManager logo upload', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    attachmentStatus.hydrate({ enabled: true, writable: true });
    themes.getAll.mockResolvedValue([editableTheme]);
    themes.getActive.mockResolvedValue(editableTheme);
    themes.update.mockImplementation((_id, data) =>
      Promise.resolve({ ...editableTheme, ...data })
    );
  });

  it('uploads a logo on edit and persists its URL with the theme', async () => {
    attachments.upload.mockResolvedValue({ success: true, logo_url: '/api/portal-assets/31' });

    render(ThemeManager);

    await screen.findByText('Acme');
    await fireEvent.click(screen.getByRole('button', { name: 'common.edit' }));

    const fileInput = document.querySelector('input[type="file"]');
    const file = new File(['png-bytes'], 'logo.png', { type: 'image/png' });
    await fireEvent.change(fileInput, { target: { files: [file] } });

    await waitFor(() => {
      expect(attachments.upload).toHaveBeenCalledTimes(1);
    });
    expect(uploadPayload().entityType).toBe('theme_logo');
    expect(screen.getByAltText('Current logo')).toHaveAttribute(
      'src',
      '/api/portal-assets/31'
    );

    await fireEvent.submit(
      fileInput.closest('form') ?? screen.getByRole('button', { name: 'common.save' })
    );

    await waitFor(() => {
      expect(themes.update).toHaveBeenCalledWith(
        7,
        expect.objectContaining({ logo_url: '/api/portal-assets/31' })
      );
    });
  });

  it('uploads a dark mode logo on edit and persists it independently', async () => {
    attachments.upload.mockResolvedValue({ success: true, logo_url: '/api/portal-assets/41' });

    render(ThemeManager);
    await screen.findByText('Acme');
    await fireEvent.click(screen.getByRole('button', { name: 'common.edit' }));

    const fileInputs = document.querySelectorAll('input[type="file"]');
    expect(fileInputs).toHaveLength(2);
    const file = new File(['png-bytes'], 'dark-logo.png', { type: 'image/png' });
    await fireEvent.change(fileInputs[1], { target: { files: [file] } });

    await waitFor(() => {
      expect(attachments.upload).toHaveBeenCalledTimes(1);
    });
    expect(uploadPayload().entityType).toBe('theme_logo');
    expect(screen.getByAltText('Current logo')).toHaveAttribute(
      'src',
      '/api/portal-assets/41'
    );

    await fireEvent.submit(
      fileInputs[1].closest('form') ?? screen.getByRole('button', { name: 'common.save' })
    );

    await waitFor(() => {
      expect(themes.update).toHaveBeenCalledWith(
        7,
        expect.objectContaining({ logo_url_dark: '/api/portal-assets/41', logo_url: '' })
      );
    });
  });

  it('removes the logo before saving so the theme row is cleared', async () => {
    themes.getAll.mockResolvedValue([{ ...editableTheme, logo_url: '/api/portal-assets/31' }]);

    render(ThemeManager);
    await screen.findByText('Acme');
    await fireEvent.click(screen.getByRole('button', { name: 'common.edit' }));

    await screen.findByAltText('Current logo');
    await fireEvent.click(screen.getByRole('button', { name: 'workspaceSettings.remove' }));

    await waitFor(() => {
      expect(screen.queryByAltText('Current logo')).not.toBeInTheDocument();
    });

    const fileInput = document.querySelector('input[type="file"]');
    await fireEvent.submit(
      fileInput.closest('form') ?? screen.getByRole('button', { name: 'common.save' })
    );

    await waitFor(() => {
      expect(themes.update).toHaveBeenCalledWith(
        7,
        expect.objectContaining({ logo_url: '' })
      );
    });
  });

  it('disables uploading when attachments are turned off', async () => {
    attachmentStatus.hydrate({ enabled: false, writable: true });

    render(ThemeManager);
    await screen.findByText('Acme');
    await fireEvent.click(screen.getByRole('button', { name: 'common.edit' }));

    const uploadButtons = await screen.findAllByRole('button', { name: 'lookAndFeel.uploadLogo' });
    expect(uploadButtons).toHaveLength(2);
    for (const uploadButton of uploadButtons) {
      expect(uploadButton).toBeDisabled();
    }
    expect(screen.getAllByText('workspaceSettings.attachmentsRequired')).toHaveLength(2);
    for (const fileInput of document.querySelectorAll('input[type="file"]')) {
      expect(fileInput).toBeDisabled();
    }
  });
});
