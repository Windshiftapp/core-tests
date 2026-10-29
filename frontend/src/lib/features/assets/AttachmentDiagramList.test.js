import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, test, vi } from 'vitest';

const getContentUrl = vi.hoisted(() => vi.fn((id) => `/api/v2/attachments/${id}/content`));
const getDownloadUrl = vi.hoisted(() => vi.fn((id) => `/api/v2/attachments/${id}/download`));
const getThumbnailUrl = vi.hoisted(() => vi.fn((id) => `/api/v2/attachments/${id}/thumbnail`));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('../../stores/toasts.svelte.js', () => ({
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
});
