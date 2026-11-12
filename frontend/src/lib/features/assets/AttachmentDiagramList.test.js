import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, test, vi } from 'vitest';

const getContentUrl = vi.hoisted(() => vi.fn((id) => `/api/v2/attachments/${id}/content`));
const getDownloadUrl = vi.hoisted(() => vi.fn((id) => `/api/v2/attachments/${id}/download`));
const getThumbnailUrl = vi.hoisted(() => vi.fn((id) => `/api/v2/attachments/${id}/thumbnail`));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('../../stores/toasts.svelte.js', () => ({
  surfaceError: vi.fn(),
  errorToast: vi.fn(),
}));

vi.mock('../../api/misc.js', () => ({
  attachments: { getContentUrl, getDownloadUrl, getThumbnailUrl },
}));

import AttachmentDiagramList from './AttachmentDiagramList.svelte';
import { attachments } from '../../api/misc.js';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('AttachmentDiagramList diagrams', () => {
  test('renders existing diagrams without a generic load control', () => {
    render(AttachmentDiagramList, {
      diagrams: [{ id: 5, name: 'Architecture', type: 'excalidraw' }],
    });

    expect(screen.getByText('Architecture')).toBeInTheDocument();
    expect(screen.queryByTestId('load-item-diagrams')).not.toBeInTheDocument();
  });
});

describe('AttachmentDiagramList visibility toggle', () => {
  const attachment = {
    id: 11,
    mime_type: 'image/png',
    original_filename: 'evidence.png',
    file_size: 2048,
    has_thumbnail: false,
    is_internal: false,
  };

  test('is absent unless the viewer can toggle visibility', () => {
    render(AttachmentDiagramList, { attachments: [attachment] });

    expect(screen.queryByTestId('item-attachment-visibility-11')).not.toBeInTheDocument();
  });

  test('invokes the toggle handler with the attachment', async () => {
    const ontogglevisibility = vi.fn();
    render(AttachmentDiagramList, {
      attachments: [attachment],
      canToggleVisibility: true,
      ontogglevisibility,
    });

    const toggle = screen.getByTestId('item-attachment-visibility-11');
    expect(toggle).toHaveAttribute('aria-label', 'assets.hideFromCustomer');

    await fireEvent.click(toggle);

    expect(ontogglevisibility).toHaveBeenCalledWith(attachment);
  });

  test('labels an internal attachment as hidden from the customer', () => {
    render(AttachmentDiagramList, {
      attachments: [{ ...attachment, id: 12, is_internal: true }],
      canToggleVisibility: true,
      ontogglevisibility: vi.fn(),
    });

    expect(screen.getByTestId('item-attachment-visibility-12')).toHaveAttribute(
      'aria-label',
      'assets.showToCustomer'
    );
  });
});

describe('AttachmentDiagramList lightbox', () => {
  test('renders PDFs from the content endpoint and downloads from the download endpoint', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      blob: () => Promise.resolve(new Blob(['%PDF-1.4'], { type: 'application/pdf' })),
    });
    vi.stubGlobal('fetch', fetchMock);
    globalThis.URL.createObjectURL = vi.fn(() => 'blob:test');
    globalThis.URL.revokeObjectURL = vi.fn();
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});

    render(AttachmentDiagramList, {
      attachments: [
        {
          id: 42,
          mime_type: 'application/pdf',
          original_filename: 'pricing.pdf',
          file_size: 1024,
          has_thumbnail: false,
        },
      ],
    });

    await fireEvent.click(screen.getByText('pricing.pdf'));

    await waitFor(() =>
      expect(document.querySelector('iframe')).toHaveAttribute(
        'src',
        '/api/v2/attachments/42/content'
      )
    );

    await fireEvent.click(screen.getByTestId('attachment-lightbox-download'));

    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith('/api/v2/attachments/42/download')
    );
    expect(getContentUrl).toHaveBeenCalledWith(42);
    expect(getDownloadUrl).toHaveBeenCalledWith(42);
  });

  test('closes on Escape and on a backdrop click but not on an inside click', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: true,
        blob: () => Promise.resolve(new Blob(['x'], { type: 'image/png' })),
      })
    );

    render(AttachmentDiagramList, {
      attachments: [
        {
          id: 7,
          mime_type: 'image/png',
          original_filename: 'shot.png',
          file_size: 10,
          has_thumbnail: false,
        },
      ],
    });

    await fireEvent.click(screen.getByText('shot.png'));
    const dialog = await screen.findByRole('dialog');

    // A click on the content does not dismiss the lightbox.
    await fireEvent.click(document.querySelector('img[alt="shot.png"]'));
    expect(screen.getByRole('dialog')).toBeInTheDocument();

    // A click on the backdrop does.
    await fireEvent.click(dialog);
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());

    // Escape closes it too.
    await fireEvent.click(screen.getByText('shot.png'));
    const reopened = await screen.findByRole('dialog');
    await fireEvent.keyDown(reopened, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });
});
