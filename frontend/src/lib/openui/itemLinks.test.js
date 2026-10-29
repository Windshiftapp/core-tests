import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';

vi.mock('../stores/i18n.svelte.js', () => ({ t: (key) => key }));

import CustomWidgetRenderer from './CustomWidgetRenderer.svelte';
import { itemSpec, sourceKey } from './querySpec.js';

const query = 'status_completed = false';

function sourcesWith(rows) {
  return {
    [sourceKey(itemSpec({ query }))]: { status: 'ready', rows, total: rows.length, truncated: false },
  };
}

function renderTable(rows, { openItem = vi.fn() } = {}) {
  const program = `root = Card([ItemsTable("${query}", "key,title,status")])`;
  const result = render(CustomWidgetRenderer, { program, sources: sourcesWith(rows), openItem });
  return { ...result, openItem };
}

const linkedRow = {
  id: 42,
  key: 'SP-27',
  title: 'Vulnerability ticket workflow',
  status: 'Open',
  workspace_id: 3,
};

describe('ItemsTable links', () => {
  it('renders the key as a deep link to the item', () => {
    renderTable([linkedRow]);

    const link = screen.getByRole('link', { name: 'SP-27' });
    expect(link).toHaveAttribute('href', '/workspaces/3/items/42');
  });

  it('renders the title as a link too', () => {
    renderTable([linkedRow]);

    const link = screen.getByRole('link', { name: 'Vulnerability ticket workflow' });
    expect(link).toHaveAttribute('href', '/workspaces/3/items/42');
  });

  it('opens the item modal on a plain click instead of navigating', async () => {
    const { openItem } = renderTable([linkedRow]);

    const link = screen.getByRole('link', { name: 'SP-27' });
    await fireEvent.click(link);

    expect(openItem).toHaveBeenCalledWith(42);
  });

  it('keeps a real href so modified clicks still open a new tab', () => {
    renderTable([linkedRow]);

    // The href is what the browser uses for cmd/ctrl/shift/middle click; the
    // shared Link component leaves those events alone.
    expect(screen.getByRole('link', { name: 'SP-27' })).toHaveAttribute(
      'href',
      '/workspaces/3/items/42'
    );
  });

  it('leaves the key as text when the row has no workspace', () => {
    renderTable([{ id: 42, key: 'SP-27', title: 'No workspace', status: 'Open' }]);

    expect(screen.queryByRole('link', { name: 'SP-27' })).not.toBeInTheDocument();
    expect(screen.getByTestId('openui-items-table')).toHaveTextContent('SP-27');
  });

  it('does not link asset rows, which have no item route', () => {
    const assetQuery = '';
    const program = `root = Card([ItemsTable("", "key,title,status", "", 10, "asset")])`;
    render(CustomWidgetRenderer, {
      program,
      sources: {
        [sourceKey({ entity: 'asset', ql: assetQuery, limit: 10 })]: {
          status: 'ready',
          rows: [{ id: 7, key: 'A-7', title: 'Laptop', status: 'Active', workspace_id: 3 }],
          total: 1,
          truncated: false,
        },
      },
    });

    expect(screen.queryByRole('link', { name: 'A-7' })).not.toBeInTheDocument();
    expect(screen.getByTestId('openui-items-table')).toHaveTextContent('A-7');
  });
});
