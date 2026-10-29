import { createParser } from '@openuidev/svelte-lang';
import { describe, expect, it } from 'vitest';

import { componentNames, widgetLibrary } from './library.js';

// OpenUI Lang uses positional arguments, so the fixture below is written the
// way a generated program would be.
const fixtureProgram = `root = Card([
  Title("Open work"),
  MetricRow([
    Metric("Open", "status_completed = false"),
    Metric("Overdue", "due_date < 0d")
  ]),
  ItemsTable("status_completed = false", "key,title,status", "-updated_at")
])`;

describe('OpenUI widget library', () => {
  it('exposes every component the server allow-list accepts', () => {
    const defined = Object.keys(widgetLibrary.components).sort();
    expect(defined).toEqual([...componentNames].sort());
  });

  it('parses a fixture program without structural errors', () => {
    const parser = createParser(widgetLibrary.toJSONSchema());
    const result = parser.parse(fixtureProgram);
    expect(result.meta?.errors ?? []).toEqual([]);
  });

  it('reports an unknown component without throwing', () => {
    const parser = createParser(widgetLibrary.toJSONSchema());
    const result = parser.parse('root = Wibble("x")');
    const codes = (result.meta?.errors ?? []).map((error) => error.code);
    expect(codes).toContain('unknown-component');
  });

  it('rejects named arguments, which the language does not support', () => {
    const parser = createParser(widgetLibrary.toJSONSchema());
    const result = parser.parse('root = Card(children=[Title(text="x")])');
    const codes = (result.meta?.errors ?? []).map((error) => error.code);
    expect(codes).not.toEqual([]);
    expect(codes.some((code) => code === 'excess-args' || code === 'null-required')).toBe(true);
  });

  it('generates a prompt that names every component and the CQL scope rule', () => {
    const prompt = widgetLibrary.prompt({ preamble: 'You build dashboard widgets.' });
    for (const name of componentNames) {
      expect(prompt).toContain(name);
    }
    expect(prompt).toContain('CQL');
  });
});
