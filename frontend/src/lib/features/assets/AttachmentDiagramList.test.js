import { render, screen } from '@testing-library/svelte';
import { describe, expect, test, vi } from 'vitest';

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('../../stores/toasts.svelte.js', () => ({
  errorToast: vi.fn(),
}));

import AttachmentDiagramList from './AttachmentDiagramList.svelte';

describe('AttachmentDiagramList diagrams', () => {
  test('renders existing diagrams without a generic load control', () => {
    render(AttachmentDiagramList, {
      diagrams: [{ id: 5, name: 'Architecture', type: 'excalidraw' }],
    });

    expect(screen.getByText('Architecture')).toBeInTheDocument();
    expect(screen.queryByTestId('load-item-diagrams')).not.toBeInTheDocument();
  });
});
