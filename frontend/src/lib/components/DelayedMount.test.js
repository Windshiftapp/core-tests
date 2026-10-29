/** @vitest-environment jsdom */

import { render, screen } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

import DelayedMount from './DelayedMount.svelte';

const content = createRawSnippet(() => ({
  render: () => '<p data-testid="delayed-content">ready</p>',
}));

afterEach(() => {
  vi.useRealTimers();
});

describe('DelayedMount', () => {
  it('renders children only after the delay has elapsed', async () => {
    vi.useFakeTimers();
    render(DelayedMount, { delay: 100, children: content });

    expect(screen.queryByTestId('delayed-content')).not.toBeInTheDocument();

    await vi.advanceTimersByTimeAsync(150);

    expect(screen.getByTestId('delayed-content')).toBeInTheDocument();
  });

  it('never renders children when it unmounts before the delay', async () => {
    vi.useFakeTimers();
    const { unmount } = render(DelayedMount, { delay: 100, children: content });

    unmount();
    await vi.advanceTimersByTimeAsync(500);

    expect(screen.queryByTestId('delayed-content')).not.toBeInTheDocument();
  });
});
