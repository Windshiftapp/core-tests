import { render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';

vi.mock('../stores/i18n.svelte.js', () => ({ t: (key) => key }));

import CustomWidgetRenderer from './CustomWidgetRenderer.svelte';
import { itemSpec, sourceKey } from './querySpec.js';

const PAYLOAD = '<img src=x onerror="window.__xss=1">';

beforeAll(() => {
  if (!globalThis.ResizeObserver) {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disable() {}
      disconnect() {}
    };
  }
});

function renderProgram(program, sources = {}) {
  return render(CustomWidgetRenderer, { program, sources });
}

function expectNoScript(container) {
  expect(container.querySelector('script')).toBeNull();
  expect(container.querySelector('img')).toBeNull();
  expect(container.querySelector('iframe')).toBeNull();
  // No element may carry an event handler attribute sourced from data.
  for (const element of container.querySelectorAll('*')) {
    for (const attr of element.attributes) {
      expect(attr.name.startsWith('on')).toBe(false);
    }
  }
}

describe('widget rendering cannot inject script', () => {
  it('escapes markup placed in text props', () => {
    const { container } = renderProgram(`root = Card([Title("${PAYLOAD}"), Text("${PAYLOAD}")])`);

    expectNoScript(container);
    // The markup is shown as text, not parsed.
    expect(container.textContent).toContain('<img src=x');
  });

  it('escapes markup coming from item data', () => {
    const query = 'status_completed = false';
    const rows = [
      { id: 1, key: 'WI-1', title: PAYLOAD, status: PAYLOAD, assignee: PAYLOAD },
    ];
    const sources = {
      [sourceKey(itemSpec({ query }))]: { status: 'ready', rows, total: 1, truncated: false },
    };

    const { container } = renderProgram(
      `root = Card([ItemsTable("${query}", "key,title,status,assignee")])`,
      sources
    );

    expectNoScript(container);
    expect(container.textContent).toContain('<img src=x');
  });

  it('escapes markup used as a column header', () => {
    const query = 'status_completed = false';
    const sources = {
      [sourceKey(itemSpec({ query }))]: { status: 'ready', rows: [], total: 0, truncated: false },
    };

    const { container } = renderProgram(
      `root = Card([ItemsTable("${query}", "key,${PAYLOAD}")])`,
      sources
    );

    expectNoScript(container);
  });

  it('refuses a javascript:, data:, or off-site link', () => {
    const { container } = renderProgram(
      `root = Card([
        LinkButton("js", "javascript:window.__xss=1"),
        LinkButton("proto", "//evil.example"),
        LinkButton("abs", "https://evil.example"),
        LinkButton("data", "data:text/html,<script>alert(1)</script>")
      ])`
    );

    expectNoScript(container);
    // None of them becomes a link at all, so there is nothing to click.
    expect(container.querySelectorAll('a')).toHaveLength(0);
  });

  it('still renders an ordinary in-app link', () => {
    const { container } = renderProgram(`root = Card([LinkButton("board", "/board")])`);

    const anchors = container.querySelectorAll('a');
    expect(anchors).toHaveLength(1);
    expect(anchors[0].getAttribute('href')).toBe('/board');
  });

  it('renders a schema-violating prop value as inert text', () => {
    // The OpenUI parser reports schema violations but still renders, so a prop
    // outside its enum reaches the component. It must not become markup.
    const { container } = renderProgram(
      `root = Card([Badge("${PAYLOAD}", "${PAYLOAD}"), Title("x", "${PAYLOAD}")])`
    );

    expectNoScript(container);
  });

  it('escapes markup used as a grouping label', () => {
    const query = 'status_completed = false';
    const sources = {
      [sourceKey(itemSpec({ query }))]: {
        status: 'ready',
        rows: [{ id: 1, title: 'a', status: PAYLOAD }],
        total: 1,
        truncated: false,
      },
    };

    const { container } = renderProgram(
      `root = Card([ProgressList("${query}", "status", "", 100)])`,
      sources
    );

    expectNoScript(container);
  });

  it('escapes markup in a graph node title', () => {
    const graphProgram = `root = Card([BipartiteList("", "", "", 1000, "", "", 100)])`;
    const { sourceKey: key } = {};
    const sources = {};
    const specKey = JSON.stringify({
      entity: 'links',
      links: { item: { limit: 1000 }, asset: { limit: 1000 }, max_edges: 100 },
    });
    sources[specKey] = {
      status: 'ready',
      graph: {
        nodes: [
          { id: 'item-1', entity_id: 1, type: 'item', title: PAYLOAD, key: 'WI-1' },
          { id: 'asset-1', entity_id: 1, type: 'asset', title: PAYLOAD, key: 'A-1' },
        ],
        edges: [{ id: 'e1', source: 'item-1', target: 'asset-1', kind: 'link' }],
        counts: { items: 1, assets: 1, edges: 1 },
      },
    };

    const { container } = renderProgram(graphProgram, sources);
    expectNoScript(container);
  });
});
