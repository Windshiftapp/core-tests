import { fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../stores/i18n.svelte.js', () => ({ t: (key) => key }));

import DashboardItemRow from './DashboardItemRow.svelte';

const href = '/workspaces/1/items/7';

afterEach(() => {
  vi.clearAllMocks();
});

describe('DashboardItemRow', () => {
  it('renders a real link so the item can be opened in a new tab', () => {
    render(DashboardItemRow, {
      props: { title: 'Ship it', itemKey: 'WI-7', href, onclick: vi.fn() },
    });

    const row = screen.getByTestId('dashboard-item-row');
    expect(row.tagName).toBe('A');
    expect(row).toHaveAttribute('href', href);
    expect(row).toHaveAttribute('data-item-key', 'WI-7');
  });

  it('diverts an unmodified left click to the handler and suppresses navigation', () => {
    const onclick = vi.fn();
    render(DashboardItemRow, {
      props: { title: 'Ship it', itemKey: 'WI-7', href, onclick },
    });

    const row = screen.getByTestId('dashboard-item-row');
    const event = new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 });
    row.dispatchEvent(event);

    expect(event.defaultPrevented).toBe(true);
    expect(onclick).toHaveBeenCalledTimes(1);
  });

  it('leaves modified and middle clicks to the browser for new tabs and windows', async () => {
    const onclick = vi.fn();
    render(DashboardItemRow, {
      props: { title: 'Ship it', itemKey: 'WI-7', href, onclick },
    });

    const row = screen.getByTestId('dashboard-item-row');

    await fireEvent.click(row, { ctrlKey: true });
    await fireEvent.click(row, { metaKey: true });
    await fireEvent.click(row, { shiftKey: true });
    await fireEvent.click(row, { button: 1 });

    expect(onclick).not.toHaveBeenCalled();
  });

  it('falls back to a button when no href is available', () => {
    render(DashboardItemRow, {
      props: { title: 'No link', itemKey: 'WI-8', onclick: vi.fn() },
    });

    expect(screen.getByTestId('dashboard-item-row').tagName).toBe('BUTTON');
  });

  it('renders the item type icon alongside the item key', () => {
    render(DashboardItemRow, {
      props: {
        title: 'Ship it',
        itemKey: 'WI-7',
        itemTypeName: 'Bug',
        itemTypeIcon: 'Bug',
        itemTypeColor: '#ef4444',
        href,
        onclick: vi.fn(),
      },
    });

    const icon = screen.getByTestId('dashboard-item-type-icon');
    expect(screen.getByTestId('dashboard-item-key')).toContainElement(icon);
    expect(icon.querySelector('svg')).toHaveClass('lucide-bug');
    expect(icon).toHaveAttribute('title', 'Bug');
  });

  it('omits the type icon when the row has no item type', () => {
    render(DashboardItemRow, {
      props: { title: 'Ship it', itemKey: 'WI-7', href, onclick: vi.fn() },
    });

    expect(screen.queryByTestId('dashboard-item-type-icon')).not.toBeInTheDocument();
  });
});
