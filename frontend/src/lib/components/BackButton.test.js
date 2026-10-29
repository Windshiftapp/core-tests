import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';

import BackButton from './BackButton.svelte';

describe('BackButton', () => {
  it('renders a router-aware link when href is provided', () => {
    render(BackButton, {
      props: { href: '/agents', label: 'Back to agents', testid: 'back-link' },
    });

    const link = screen.getByTestId('back-link');
    expect(link.tagName).toBe('A');
    expect(link).toHaveAttribute('href', '/agents');
    expect(link).toHaveTextContent('Back to agents');
  });

  it('renders a button and invokes onclick', async () => {
    const onclick = vi.fn();
    render(BackButton, {
      props: { onclick, label: 'Back to iterations', testid: 'back-button' },
    });

    const button = screen.getByTestId('back-button');
    expect(button.tagName).toBe('BUTTON');
    await fireEvent.click(button);
    expect(onclick).toHaveBeenCalledTimes(1);
  });

  it('uses the accessible fallback name for an icon-only control', () => {
    render(BackButton, { props: { onclick: vi.fn(), testid: 'back-icon' } });

    expect(screen.getByTestId('back-icon')).toHaveAttribute('aria-label', 'Back');
  });

  it('exposes the tooltip as the native title and accessible name', () => {
    render(BackButton, {
      props: { onclick: vi.fn(), tooltip: 'Back to reports', testid: 'back-tooltip' },
    });

    const button = screen.getByTestId('back-tooltip');
    expect(button).toHaveAttribute('title', 'Back to reports');
    expect(button).toHaveAttribute('aria-label', 'Back to reports');
  });

  it('blocks activation while disabled', async () => {
    const onclick = vi.fn();
    render(BackButton, {
      props: { onclick, disabled: true, testid: 'back-disabled' },
    });

    const button = screen.getByTestId('back-disabled');
    expect(button).toBeDisabled();
    await fireEvent.click(button);
    expect(onclick).not.toHaveBeenCalled();
  });
});
