import { render, screen } from '@testing-library/svelte';
import MoreHorizontal from '@lucide/svelte/icons/more-horizontal';
import { describe, expect, it, vi } from 'vitest';

vi.mock('../stores/i18n.svelte.js', () => ({ t: (key) => key }));

import DropdownMenu from './DropdownMenu.svelte';

// Regression: the colored-circle branch hardcoded `text-white` on the icon, so
// any caller passing a light background got an invisible trigger — which is
// exactly what made the dashboard widget menu unreachable.
describe('DropdownMenu trigger icon colour', () => {
  it('derives the icon colour from the background instead of hardcoding white', () => {
    const { container } = render(DropdownMenu, {
      triggerIcon: MoreHorizontal,
      triggerIconBgColor: '#ffffff',
      triggerTestid: 'menu-trigger',
      items: [],
    });

    const trigger = screen.getByTestId('menu-trigger');
    const icon = trigger.querySelector('svg');
    expect(icon).not.toBeNull();

    // A token, not a literal colour.
    expect(icon.getAttribute('style') ?? '').toContain('var(--ds-');
    expect(container.innerHTML).not.toContain('text-white');
  });

  it('keeps a readable colour on a dark background', () => {
    render(DropdownMenu, {
      triggerIcon: MoreHorizontal,
      triggerIconBgColor: '#111827',
      triggerTestid: 'dark-trigger',
      items: [],
    });

    const icon = screen.getByTestId('dark-trigger').querySelector('svg');
    expect(icon.getAttribute('style') ?? '').toContain('var(--ds-text-inverse)');
  });
});
