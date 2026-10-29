import { render, screen } from '@testing-library/svelte';
import { beforeAll, describe, expect, it, vi } from 'vitest';

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

import CustomWidgetRenderer from './CustomWidgetRenderer.svelte';
import { itemSpec, linksSpec, sourceKey } from './querySpec.js';

const rows = [
  { id: 1, key: 'WI-1', title: 'Fix login', status: 'Open', assignee: 'Ada' },
  { id: 2, key: 'WI-2', title: 'Ship it', status: 'Open', assignee: 'Bob' },
];

const program = `root = Card([
  Title("Open work"),
  Metric("Open", "status = 'Open'"),
  ItemsTable("status = 'Open'", "key,title,status,assignee")
])`;

function sourcesFor({ total = rows.length, truncated = false } = {}) {
  return {
    [sourceKey(itemSpec({ query: "status = 'Open'" }))]: {
      status: 'ready',
      rows,
      total,
      truncated,
    },
  };
}

beforeAll(() => {
  // The shared Chart component observes its container; jsdom has no
  // ResizeObserver, so the chart would throw before the assertions run.
  if (!globalThis.ResizeObserver) {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  }
});

describe('CustomWidgetRenderer', () => {
  it('renders fixture values from the injected data context', () => {
    render(CustomWidgetRenderer, { program, sources: sourcesFor() });

    expect(screen.getByTestId('openui-title')).toHaveTextContent('Open work');
    expect(screen.getByTestId('openui-metric')).toHaveTextContent('2');
    expect(screen.getByTestId('openui-items-table')).toHaveTextContent('Fix login');
    expect(screen.getByTestId('openui-items-table')).toHaveTextContent('Ada');
  });

  it('shows a truncation badge only when the row sample is incomplete', () => {
    const { unmount } = render(CustomWidgetRenderer, {
      program,
      sources: sourcesFor({ total: 250, truncated: true }),
    });
    expect(screen.getByTestId('openui-truncation-badge')).toBeInTheDocument();
    unmount();

    render(CustomWidgetRenderer, { program, sources: sourcesFor() });
    expect(screen.queryByTestId('openui-truncation-badge')).not.toBeInTheDocument();
  });

  it('renders an inline error instead of failing when a source errored', () => {
    render(CustomWidgetRenderer, {
      program,
      sources: {
        [sourceKey(itemSpec({ query: "status = 'Open'" }))]: {
          status: 'error',
          error: { message: 'denied' },
        },
      },
    });

    expect(screen.getByTestId('openui-source-error')).toHaveTextContent('denied');
    // The static parts of the program still render.
    expect(screen.getByTestId('openui-title')).toHaveTextContent('Open work');
  });

  it('keeps rendering known components when the program contains an unknown one', () => {
    const onParseErrors = vi.fn();
    render(CustomWidgetRenderer, {
      program: `root = Card([Title("Still here"), Wibble("x")])`,
      sources: {},
      onParseErrors,
    });

    expect(screen.getByTestId('openui-title')).toHaveTextContent('Still here');
    const codes = onParseErrors.mock.calls.flatMap(([errors]) => errors.map((error) => error.code));
    expect(codes).toContain('unknown-component');
  });

  it('renders an edge slot as a grouped link list', () => {
    const graphProgram = `root = Card([BipartiteList("status_completed = false", "", "updated_at", 1000, "", "", 100)])`;
    const graphKey = sourceKey(
      linksSpec({
        itemQuery: 'status_completed = false',
        itemSort: 'updated_at',
        itemLimit: 1000,
        assetLimit: 1000,
        maxEdges: 100,
      })
    );

    render(CustomWidgetRenderer, {
      program: graphProgram,
      sources: {
        [graphKey]: {
          status: 'ready',
          graph: {
            nodes: [
              { id: 'item-1', entity_id: 1, type: 'item', title: 'Fix login', key: 'WI-1' },
              { id: 'item-2', entity_id: 2, type: 'item', title: 'Ship it', key: 'WI-2' },
              { id: 'asset-11', entity_id: 11, type: 'asset', title: 'Laptop', key: 'A-11' },
            ],
            edges: [
              { id: 'e1', source: 'item-1', target: 'asset-11', kind: 'link' },
              { id: 'e2', source: 'item-1', target: 'asset-11', kind: 'field_reference' },
            ],
            counts: { items: 2, assets: 1, edges: 2 },
          },
        },
      },
    });

    const list = screen.getByTestId('openui-bipartite-list');
    expect(list).toHaveTextContent('Fix login');
    expect(list).toHaveTextContent('Laptop');
    // The item with no links is not listed.
    expect(list).not.toHaveTextContent('Ship it');
  });

  it('shows the empty state when an edge slot has no links', () => {
    const graphProgram = `root = Card([BipartiteList("", "", "", 1000, "", "", 100)])`;
    const graphKey = sourceKey(
      linksSpec({ itemLimit: 1000, assetLimit: 1000, maxEdges: 100 })
    );

    render(CustomWidgetRenderer, {
      program: graphProgram,
      sources: {
        [graphKey]: {
          status: 'ready',
          graph: { nodes: [], edges: [], counts: { items: 0, assets: 0, edges: 0 } },
        },
      },
    });

    expect(screen.getByTestId('openui-bipartite-empty')).toBeInTheDocument();
  });

  it('renders a PieChart program as slices of the grouped rows', () => {
    const pieProgram = `root = Card([PieChart("status = 'Open'", "assignee", "", 1000, "count", "", 8)])`;
    const pieRows = [
      { id: 1, assignee: 'Ada' },
      { id: 2, assignee: 'Ada' },
      { id: 3, assignee: 'Ada' },
      { id: 4, assignee: 'Bob' },
    ];

    render(CustomWidgetRenderer, {
      program: pieProgram,
      sources: {
        [sourceKey(itemSpec({ query: "status = 'Open'", limit: 1000 }))]: {
          status: 'ready',
          rows: pieRows,
        },
      },
    });

    // The pie is a pie, not a bar chart: two wedges and a share legend.
    expect(screen.getByTestId('chart-slice-0')).toBeInTheDocument();
    expect(screen.getByTestId('chart-slice-1')).toBeInTheDocument();
    expect(screen.queryByTestId('chart-slice-2')).not.toBeInTheDocument();
    const legend = screen.getByTestId('chart-pie-legend');
    expect(legend).toHaveTextContent('Ada');
    expect(legend).toHaveTextContent('Bob');
    expect(screen.getByTestId('chart-slice-legend-0')).toHaveTextContent('75%');
  });

  it('renders a donut when the program asks for one', () => {
    const donutProgram = `root = Card([PieChart("", "status", "", 1000, "count", "", 8, true)])`;

    render(CustomWidgetRenderer, {
      program: donutProgram,
      sources: {
        [sourceKey(itemSpec({ query: '', limit: 1000 }))]: { status: 'ready', rows },
      },
    });

    expect(screen.getByTestId('chart-pie-legend')).toHaveTextContent('Open');
    // The donut prints the total in the middle of the ring.
    expect(screen.getByText('2', { selector: 'svg text' })).toBeInTheDocument();
  });

  it('drops a program title that repeats the tile heading', () => {
    render(CustomWidgetRenderer, {
      program: `root = Card([\n  Title("Open work"),\n  ItemsTable("status = 'Open'", "key,title")\n])`,
      tileTitle: 'Open work',
      sources: sourcesFor(),
    });

    // The tile heading already says "Open work"; the program must not repeat it.
    expect(screen.queryByTestId('openui-title')).not.toBeInTheDocument();
    expect(screen.getByTestId('openui-items-table')).toHaveTextContent('Fix login');
  });

  it('keeps a program title that adds information beyond the tile heading', () => {
    render(CustomWidgetRenderer, {
      program: `root = Card([\n  Title("By assignee"),\n  ItemsTable("status = 'Open'", "key,title")\n])`,
      tileTitle: 'Open work',
      sources: sourcesFor(),
    });

    expect(screen.getByTestId('openui-title')).toHaveTextContent('By assignee');
  });

  it('shows a loading skeleton before data arrives', () => {
    render(CustomWidgetRenderer, { program, sources: {} });
    expect(screen.getByTestId('openui-source-loading')).toBeInTheDocument();
  });
});
