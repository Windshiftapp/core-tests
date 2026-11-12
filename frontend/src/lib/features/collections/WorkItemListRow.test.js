import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { createRawSnippet } from 'svelte';

vi.mock('./ListCellRenderer.svelte', async () => ({
  default: (await import('./WorkItemCellStub.svelte')).default,
}));

vi.mock('../../layout/DropdownMenu.svelte', async () => ({
  default: (await import('./WorkItemActionsStub.svelte')).default,
}));

import WorkItemListRow from './WorkItemListRow.svelte';

// LazyRender gates rows behind IntersectionObserver, which the test DOM lacks;
// report the sentinel as immediately visible so the row body renders.
class ImmediateIntersectionObserver {
  constructor(callback) {
    this.callback = callback;
  }
  observe() {
    this.callback([{ isIntersecting: true }], this);
  }
  unobserve() {}
  disconnect() {}
  takeRecords() {
    return [];
  }
}

const COLUMNS = [
  { field_identifier: 'key' },
  { field_identifier: 'title' },
];

function baseProps(overrides = {}) {
  return {
    item: { id: 7, title: 'Alpha' },
    columns: COLUMNS,
    gridTemplateColumns: '1fr 2fr',
    gridMinWidth: '12rem',
    actions: [{ label: 'view' }],
    rowTestid: 'row-7',
    ...overrides,
  };
}

beforeEach(() => {
  vi.stubGlobal('IntersectionObserver', ImmediateIntersectionObserver);
});

afterEach(() => {
  vi.unstubAllGlobals();
  cleanup();
});

describe('WorkItemListRow', () => {
  test('renders one cell per column in order plus the row action menu', () => {
    render(WorkItemListRow, { props: baseProps() });

    const cells = screen.getAllByTestId('stub-cell');
    expect(cells.map((cell) => cell.getAttribute('data-column'))).toEqual(['key', 'title']);
    expect(screen.getByTestId('stub-actions')).toHaveTextContent('1');

    const row = screen.getByTestId('row-7');
    expect(row).toHaveAttribute('data-item-row');
    expect(row).toHaveAttribute('data-item-id', '7');
  });

  test('forwards the per-item edit permission to every cell', () => {
    render(WorkItemListRow, { props: baseProps({ canEdit: true }) });

    const cells = screen.getAllByTestId('stub-cell');
    expect(cells).toHaveLength(2);
    for (const cell of cells) {
      expect(cell).toHaveAttribute('data-can-edit', 'true');
    }
  });

  test('renders the leading snippet before the first data cell', () => {
    const leading = createRawSnippet(() => ({
      render: () => '<input data-testid="lead" />',
    }));

    render(WorkItemListRow, { props: baseProps({ leading }) });

    const row = screen.getByTestId('row-7');
    const lead = screen.getByTestId('lead');
    const firstCell = screen.getAllByTestId('stub-cell')[0];
    expect(row).toContainElement(lead);
    expect(lead.compareDocumentPosition(firstCell) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  test('only registers a native drag source when draggable is set', () => {
    render(WorkItemListRow, { props: baseProps({ draggable: true }) });
    expect(screen.getByTestId('row-7')).toHaveAttribute('draggable', 'true');

    cleanup();

    render(WorkItemListRow, { props: baseProps() });
    expect(screen.getByTestId('row-7')).not.toHaveAttribute('draggable');
  });
});
