import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';

vi.mock('../stores', () => ({
  attachmentStatus: { enabled: true },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

import BackgroundImageSelector from './BackgroundImageSelector.svelte';

describe('BackgroundImageSelector', () => {
  it('uses the supplied aspect ratio and reports the selected preset', async () => {
    const onSelectImage = vi.fn();

    render(BackgroundImageSelector, {
      currentImageUrl: null,
      onSelectImage,
      presetAspectRatio: '16 / 9',
    });

    const preset = screen.getByTitle('Gradient Waves');
    expect(preset).toHaveStyle({ aspectRatio: '16 / 9' });

    await fireEvent.click(preset);

    expect(onSelectImage).toHaveBeenCalledWith(
      'https://images.unsplash.com/photo-1557682250-33bd709cbe85?w=1920&q=80'
    );
  });

  it('switches categories and lets the current image be removed', async () => {
    const onRemoveImage = vi.fn();

    render(BackgroundImageSelector, {
      currentImageUrl: 'https://example.test/background.png',
      onRemoveImage,
    });

    await fireEvent.click(screen.getByRole('button', { name: 'Nature' }));
    expect(screen.getByTitle('Mountain Lake')).toBeInTheDocument();

    await fireEvent.click(screen.getByRole('button', { name: 'workspaceSettings.remove' }));
    expect(onRemoveImage).toHaveBeenCalledOnce();
  });
});
