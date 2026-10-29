import { render, screen } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import GradientSelector from './GradientSelector.svelte';

/**
 * The dim overlay sits inside a themed surface (the portal customize panel),
 * which scopes its own color mode. The scrim must follow that surface's theme
 * rather than the document root, so it stays legible when the app and the
 * portal disagree.
 */

const gradients = [
  { name: 'None', value: '' },
  { name: 'Sunset', value: 'linear-gradient(90deg, #f00, #00f)' },
];

describe('GradientSelector scrim', () => {
  it('dims the swatches with a light scrim in a light surface', () => {
    render(GradientSelector, {
      gradients,
      selectedIndex: 0,
      hasBackgroundImage: true,
      isDarkMode: false,
    });

    expect(screen.getByTestId('gradient-scrim')).toHaveStyle({
      backgroundColor: 'rgba(255, 255, 255, 0.5)',
    });
  });

  it('dims the swatches with a dark scrim in a dark surface', () => {
    render(GradientSelector, {
      gradients,
      selectedIndex: 0,
      hasBackgroundImage: true,
      isDarkMode: true,
    });

    expect(screen.getByTestId('gradient-scrim')).toHaveStyle({
      backgroundColor: 'rgba(0, 0, 0, 0.5)',
    });
  });
});
