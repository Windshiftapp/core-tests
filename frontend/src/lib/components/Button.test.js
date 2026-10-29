import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';

import Button from './Button.svelte';

describe('Button pending state', () => {
  it('blocks activation while loading', async () => {
    const onclick = vi.fn();
    render(Button, {
      props: {
        dataTestid: 'pending-button',
        loading: true,
        onclick,
      },
    });

    const button = screen.getByTestId('pending-button');
    expect(button).toBeDisabled();

    await fireEvent.click(button);
    expect(onclick).not.toHaveBeenCalled();
  });
});

describe('Button toggle state', () => {
  it('reports the unpressed toggle state', () => {
    render(Button, {
      props: { pressed: false, dataTestid: 'toggle-button' },
    });

    expect(screen.getByTestId('toggle-button')).toHaveAttribute('aria-pressed', 'false');
  });

  it('reports the pressed toggle state', () => {
    render(Button, {
      props: { pressed: true, dataTestid: 'toggle-button' },
    });

    expect(screen.getByTestId('toggle-button')).toHaveAttribute('aria-pressed', 'true');
  });

  it('omits aria-pressed for buttons that are not toggles', () => {
    render(Button, { props: { dataTestid: 'plain-button' } });

    expect(screen.getByTestId('plain-button')).not.toHaveAttribute('aria-pressed');
  });
});
