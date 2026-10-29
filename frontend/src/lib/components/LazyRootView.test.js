import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { tick } from 'svelte';
import { describe, expect, it, vi } from 'vitest';

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) =>
    ({
      'common.loading': 'Loading...',
      'errors.failedToLoad': 'Failed to load',
      'errors.NETWORK_ERROR': 'Network error. Please check your connection.',
      'common.retry': 'Retry',
    })[key] ?? key,
}));

import LazyRootView from './LazyRootView.svelte';
import Spinner from './Spinner.svelte';

describe('LazyRootView', () => {
  it('delays the loading state until the chunk load is actually slow (WI-1761)', async () => {
    vi.useFakeTimers();
    try {
      const loader = vi.fn(() => new Promise(() => {}));

      render(LazyRootView, { loader, label: 'workspace' });

      // A pending load alone must not flash the spinner.
      expect(screen.queryByTestId('root-view-loading')).not.toBeInTheDocument();

      await vi.advanceTimersByTimeAsync(200);
      await tick();

      const loadingState = screen.getByTestId('root-view-loading');
      expect(loadingState).toHaveTextContent('Loading...');
      expect(loadingState).toHaveAttribute('data-root-label', 'workspace');
      expect(loadingState.getAttribute('style')).toContain('color: var(--ds-text)');
      expect(screen.getByLabelText('Loading...').getAttribute('style')).toContain(
        'border-color: var(--ds-interactive)'
      );
      expect(loader).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it('never shows the loading state when the chunk resolves immediately', async () => {
    render(LazyRootView, {
      loader: vi.fn(async () => ({ default: Spinner })),
      label: 'workspace',
    });

    // Right after mounting, the pending state exists but shows nothing.
    expect(screen.queryByTestId('root-view-loading')).not.toBeInTheDocument();

    await waitFor(() => expect(screen.getByLabelText('Loading...')).toBeInTheDocument());
    expect(screen.queryByTestId('root-view-loading')).not.toBeInTheDocument();
  });

  it('retries a failed route chunk without reloading the application', async () => {
    const loader = vi
      .fn()
      .mockRejectedValueOnce(new Error('chunk unavailable'))
      .mockResolvedValueOnce({ default: Spinner });

    render(LazyRootView, { loader, label: 'workspace' });
    expect(await screen.findByTestId('root-view-error')).toHaveTextContent('Failed to load');

    expect(screen.getByTestId('root-view-error').getAttribute('style')).toContain(
      'color: var(--ds-text)'
    );
    await fireEvent.click(screen.getByTestId('root-view-retry'));

    await waitFor(() => expect(loader).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.queryByTestId('root-view-error')).not.toBeInTheDocument());
  });
});
