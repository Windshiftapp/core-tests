import { render, screen } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';
import DropdownMenu from './DropdownMenu.svelte';

describe('DropdownMenu trigger open state', () => {
  it('marks the trigger open without overriding an active segment surface', () => {
    render(DropdownMenu, {
      props: {
        isOpen: true,
        triggerText: 'Sort: Bubble',
        triggerActive: true,
        triggerTestid: 'sort-trigger',
        items: [{ id: 'rank', title: 'Rank mode', onClick: () => {} }],
      },
    });

    const trigger = screen.getByTestId('sort-trigger');
    expect(trigger).toHaveAttribute('data-open', 'true');
    expect(trigger).toHaveAttribute('data-active', 'true');
    expect(trigger.className).toContain('menu-trigger');
    expect(trigger.getAttribute('style') || '').not.toContain('background-color');
  });

  it('omits data-active for a trigger that is not an active segment', () => {
    render(DropdownMenu, {
      props: {
        isOpen: true,
        triggerText: 'Sort',
        triggerTestid: 'sort-trigger',
        items: [{ id: 'rank', title: 'Rank mode', onClick: () => {} }],
      },
    });

    const trigger = screen.getByTestId('sort-trigger');
    expect(trigger).toHaveAttribute('data-open', 'true');
    expect(trigger).not.toHaveAttribute('data-active');
  });
});

describe('DropdownMenu selected items', () => {
  it('keeps labels aligned and places the selected checkmark after the label', () => {
    render(DropdownMenu, {
      props: {
        isOpen: true,
        triggerText: 'Width',
        items: [
          { id: 'third', title: 'Third', onClick: () => {} },
          { id: 'two-thirds', title: 'Two-thirds', selected: true, onClick: () => {} },
          { id: 'full', title: 'Full', onClick: () => {} },
        ],
      },
    });

    const menu = screen.getByRole('menu');
    const items = [...menu.querySelectorAll('[data-menu-item]')];

    expect(items).toHaveLength(3);
    expect(items.map((item) => item.firstElementChild.className)).toEqual([
      'flex-1 text-left',
      'flex-1 text-left',
      'flex-1 text-left',
    ]);
    expect(items[1].lastElementChild.tagName).toBe('svg');
    expect(items[0].lastElementChild.tagName).toBe('DIV');
    expect(items[2].lastElementChild.tagName).toBe('DIV');
  });
});
