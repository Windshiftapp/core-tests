import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, test } from 'vitest';
import ItemTypeIcon from './ItemTypeIcon.svelte';

afterEach(() => {
  cleanup();
});

describe('ItemTypeIcon', () => {
  test('renders one consistent treatment regardless of legacy size and variant props', () => {
    const itemType = { name: 'Story', icon: 'BookOpen', color: '#059669' };

    render(ItemTypeIcon, {
      props: { itemType, size: 'xs', testId: 'app-item-type-icon' },
    });
    render(ItemTypeIcon, {
      props: {
        itemType,
        size: 'lg',
        variant: 'tinted',
        testId: 'admin-item-type-icon',
      },
    });

    const appIcon = screen.getByTestId('app-item-type-icon');
    const adminIcon = screen.getByTestId('admin-item-type-icon');
    const appGlyph = appIcon.querySelector('svg');
    const adminGlyph = adminIcon.querySelector('svg');

    expect(appIcon.style.cssText).toBe(adminIcon.style.cssText);
    expect(appGlyph).toHaveAttribute('width', '16');
    expect(appGlyph).toHaveAttribute('height', '16');
    expect(adminGlyph).toHaveAttribute('width', '16');
    expect(adminGlyph).toHaveAttribute('height', '16');
    expect(appIcon.style.color).toBe('rgb(5, 150, 105)');
    expect(appIcon.style.backgroundColor).toBe('');
  });
});
