import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, test, vi } from 'vitest';

import ItemKey from './ItemKey.svelte';

afterEach(() => cleanup());

describe('ItemKey', () => {
  test('renders the workspace key and item number', () => {
    render(ItemKey, {
      props: { item: { workspace_key: 'SUP', workspace_item_number: 12 } },
    });

    expect(screen.getByText('SUP-12')).toBeInTheDocument();
  });

  test('falls back to the workspace prop key', () => {
    render(ItemKey, {
      props: { item: { workspace_item_number: 7 }, workspace: { key: 'DEV' } },
    });

    expect(screen.getByText('DEV-7')).toBeInTheDocument();
  });

  test('falls back to the ITEM- prefix when no workspace is known', () => {
    render(ItemKey, { props: { item: { workspace_item_number: 7 } } });

    expect(screen.getByText('ITEM-7')).toBeInTheDocument();
  });

  test('prefers a preformatted itemKey string', () => {
    render(ItemKey, { props: { itemKey: 'WI-1661' } });

    expect(screen.getByText('WI-1661')).toBeInTheDocument();
  });

  test('uses the item_key field when the object carries one', () => {
    render(ItemKey, {
      props: { item: { item_key: 'OPS-3', workspace_item_number: 3 } },
    });

    expect(screen.getByText('OPS-3')).toBeInTheDocument();
  });

  test('plain variant is mono text without a chip background', () => {
    render(ItemKey, {
      props: {
        item: { workspace_key: 'SUP', workspace_item_number: 12 },
        dataTestid: 'item-key',
      },
    });

    const key = screen.getByTestId('item-key');
    expect(key.tagName).toBe('SPAN');
    expect(key).toHaveClass('font-mono');
    expect(key).not.toHaveClass('rounded');
    expect(key.getAttribute('style')).toContain('color: var(--ds-text-subtle)');
  });

  test('badge variant renders the neutral metadata chip', () => {
    render(ItemKey, {
      props: {
        item: { workspace_key: 'SUP', workspace_item_number: 12 },
        variant: 'badge',
        dataTestid: 'item-key',
      },
    });

    const key = screen.getByTestId('item-key');
    expect(key).toHaveClass('rounded');
    expect(key).toHaveClass('font-mono');
    const style = key.getAttribute('style');
    expect(style).toContain('background-color: var(--ds-background-neutral)');
    expect(style).toContain('color: var(--ds-text-subtle)');
  });

  test('renders a link and forwards the test id when href is set', () => {
    render(ItemKey, {
      props: {
        itemKey: 'SUP-12',
        href: '/workspaces/7/items/42',
        dataTestid: 'item-key-link',
      },
    });

    const link = screen.getByTestId('item-key-link');
    expect(link.tagName).toBe('A');
    expect(link).toHaveAttribute('href', '/workspaces/7/items/42');
    expect(link).toHaveTextContent('SUP-12');
  });

  test('renders a button and calls onClick when provided', async () => {
    const onClick = vi.fn();
    render(ItemKey, {
      props: { itemKey: 'SUP-12', onClick, dataTestid: 'item-key-button' },
    });

    const button = screen.getByTestId('item-key-button');
    expect(button.tagName).toBe('BUTTON');
    await fireEvent.click(button);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  test('compact size uses the smaller type scale', () => {
    render(ItemKey, {
      props: { itemKey: 'SUP-12', size: 'compact', dataTestid: 'item-key' },
    });

    expect(screen.getByTestId('item-key')).toHaveClass('text-[10px]');
  });
});
