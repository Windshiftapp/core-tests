import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { homepage } from '../api/misc.js';

vi.mock('../api/misc.js', () => ({
  homepage: {
        get: vi.fn(),
        getLayout: vi.fn(),
        updateLayout: vi.fn().mockResolvedValue({ sections: [], widgets: [] }),
      },
}));

vi.mock('../stores/i18n.svelte.js', async (importOriginal) => ({
  ...(await importOriginal()),
  t: (key) => key,
}));

const { homepageStore } = await import('../stores/homepageStore.svelte.js');
const { default: DashboardBackgroundPanel } = await import('./DashboardBackgroundPanel.svelte');

beforeEach(() => {
  homepageStore.reset();
});

afterEach(cleanup);

describe('DashboardBackgroundPanel', () => {
  it('selects a gradient and marks the swatch as pressed', async () => {
    render(DashboardBackgroundPanel);

    const none = screen.getByTestId('dashboard-gradient-swatch-0');
    const gradient = screen.getByTestId('dashboard-gradient-swatch-3');
    expect(none).toHaveAttribute('aria-pressed', 'true');

    await fireEvent.click(gradient);

    await waitFor(() => expect(homepageStore.gradient).toBe(3));
    expect(gradient).toHaveAttribute('aria-pressed', 'true');
    expect(none).toHaveAttribute('aria-pressed', 'false');
  });

  it('selects a preset image, clears the gradient, and allows removing it', async () => {
    homepageStore.selectGradient(2);
    render(DashboardBackgroundPanel);

    await fireEvent.click(screen.getByTestId('dashboard-background-preset-category-nature'));
    await fireEvent.click(screen.getByTestId('dashboard-background-preset-nature-1'));

    await waitFor(() =>
      expect(homepageStore.backgroundImageUrl).toBe(
        'https://images.unsplash.com/photo-1506905925346-21bda4d32df4?w=1920&q=80'
      )
    );
    expect(homepageStore.gradient).toBe(0);

    const remove = await screen.findByTestId('dashboard-background-remove');
    await fireEvent.click(remove);

    await waitFor(() => expect(homepageStore.backgroundImageUrl).toBeNull());
  });
});
