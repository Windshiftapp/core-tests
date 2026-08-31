import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import IconNavigationLab from './pages/IconNavigationLab.svelte';

const { createCandidate } = vi.hoisted(() => {
  const icon = '<svg viewBox="0 0 24 24" aria-hidden="true"></svg>';
  const icons = new Proxy(
    {},
    {
      get: () => icon,
    }
  );

  return {
    createCandidate: (id, label) => ({
      id,
      label,
      license: 'Test license',
      mode: 'iconoir',
      description: 'Test candidate',
      mainSize: 20,
      adminSize: 16,
      icons,
    }),
  };
});

vi.mock('./pages/icon-navigation/candidates/lucide.js', () => ({
  default: createCandidate('lucide', 'Lucide baseline'),
}));
vi.mock('./pages/icon-navigation/candidates/lucide-refined.js', () => ({
  default: createCandidate('lucide-refined', 'Lucide refined'),
}));
vi.mock('./pages/icon-navigation/candidates/iconoir.js', () => ({
  default: createCandidate('iconoir', 'Iconoir'),
}));
vi.mock('./pages/icon-navigation/candidates/carbon.js', () => ({
  default: createCandidate('carbon', 'Carbon'),
}));
vi.mock('./pages/icon-navigation/candidates/phosphor.js', () => ({
  default: createCandidate('phosphor', 'Phosphor'),
}));
vi.mock('./pages/icon-navigation/candidates/hugeicons.js', () => ({
  default: createCandidate('hugeicons', 'Hugeicons Free'),
}));

beforeEach(() => {
  document.body.innerHTML = '';
});

describe('icon navigation lab', () => {
  it('switches between lazy candidates and keeps the mock interactive', async () => {
    render(IconNavigationLab);

    const candidates = [
      ['lucide', 'Lucide baseline'],
      ['lucide-refined', 'Lucide refined'],
      ['iconoir', 'Iconoir'],
      ['carbon', 'Carbon'],
      ['phosphor', 'Phosphor'],
      ['hugeicons', 'Hugeicons Free'],
    ];

    await screen.findByTestId('icon-navigation-active-candidate');

    for (const [id, label] of candidates) {
      await fireEvent.click(screen.getByTestId(`icon-navigation-candidate-${id}`));
      await waitFor(() => {
        expect(screen.getByTestId('icon-navigation-active-candidate')).toHaveTextContent(label);
      });
    }

    await fireEvent.click(screen.getByTestId('icon-navigation-density'));

    expect(screen.getByTestId('icon-navigation-density')).toHaveTextContent(
      'Show expanded main nav'
    );
  });
});
