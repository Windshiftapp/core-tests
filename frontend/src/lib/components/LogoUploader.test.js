import { render } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';

vi.mock('../stores/attachmentStatus.svelte.js', () => ({
  attachmentStatus: { enabled: true },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key, fallback) => fallback ?? key,
}));

import LogoUploader from './LogoUploader.svelte';

// SVG is script-capable and only theme logos accept it server-side, so the
// file picker must exclude it unless the caller opts in.
describe('LogoUploader accept contract', () => {
  it('restricts the file picker to raster images by default', () => {
    const { container } = render(LogoUploader);

    const input = container.querySelector('input[type="file"]');
    expect(input).toHaveAttribute('accept', 'image/png,image/jpeg,image/gif,image/webp');
    expect(input.getAttribute('accept')).not.toContain('svg');
  });

  it('admits SVG only when the caller opts in', () => {
    const { container } = render(LogoUploader, {
      accept: 'image/png,image/jpeg,image/svg+xml,image/gif,image/webp',
    });

    expect(container.querySelector('input[type="file"]')).toHaveAttribute(
      'accept',
      'image/png,image/jpeg,image/svg+xml,image/gif,image/webp'
    );
  });
});
