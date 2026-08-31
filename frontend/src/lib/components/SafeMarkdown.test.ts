import { render, screen } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import SafeMarkdown from './SafeMarkdown.svelte';

describe('SafeMarkdown', () => {
  it('sanitizes server HTML before inserting it into the DOM', () => {
    const { container } = render(SafeMarkdown, {
      props: {
        html: '<p>Visible</p><img src="x" onerror="window.__markdownXSS = true"><script>window.__markdownXSS = true</script>',
        source: 'fallback',
        testid: 'safe-markdown',
      },
    });

    expect(screen.getByTestId('safe-markdown')).toHaveTextContent('Visible');
    expect(container.querySelector('script')).toBeNull();
    expect(container.querySelector('img')).not.toHaveAttribute('onerror');
  });

  it('uses interpolated plaintext when rendered HTML is unavailable', () => {
    render(SafeMarkdown, {
      props: {
        source: '<script>alert(1)</script>',
        testid: 'safe-markdown-fallback',
      },
    });

    expect(screen.getByTestId('safe-markdown-fallback')).toHaveTextContent(
      '<script>alert(1)</script>'
    );
    expect(document.querySelector('script')).toBeNull();
  });
});
