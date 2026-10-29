import { createParser } from '@openuidev/svelte-lang';
import { describe, expect, it } from 'vitest';

import { widgetLibrary } from './library.js';
import { stripDuplicateTitle } from './programTitle.js';

const parser = createParser(widgetLibrary.toJSONSchema());

/** Titles the parser actually produced, so the test checks rendered intent. */
function renderedTitles(program) {
	const result = parser.parse(program);
	expect(result.meta?.errors ?? []).toEqual([]);
	const children = result.root?.props?.children ?? [];
	return children.filter((child) => child?.typeName === 'Title').map((child) => child.props.text);
}

const withTitle = `root = Card([
  Title("Unassigned items older than 30 days"),
  PieChart("status_completed = false AND assignee IS NULL", "workspace", "", 1000, "count", "", 8)
])`;

describe('stripDuplicateTitle', () => {
	it('drops a leading title that repeats the tile heading', () => {
		const stripped = stripDuplicateTitle(withTitle, 'Unassigned items older than 30 days');

		expect(stripped).not.toContain('Title(');
		expect(renderedTitles(stripped)).toEqual([]);
		// The rest of the program still parses and renders.
		expect(stripped).toContain('PieChart(');
	});

	it('matches despite case, extra whitespace, and trailing punctuation', () => {
		const stripped = stripDuplicateTitle(withTitle, '  unassigned   items older than 30 days. ');

		expect(renderedTitles(stripped)).toEqual([]);
	});

	it('keeps a title that says something the tile heading does not', () => {
		expect(stripDuplicateTitle(withTitle, 'Unassigned items')).toBe(withTitle);
	});

	it('keeps a title that is not the first child', () => {
		const program = `root = Card([
  PieChart("", "workspace", "", 1000, "count", "", 8),
  Title("Unassigned items")
])`;

		expect(stripDuplicateTitle(program, 'Unassigned items')).toBe(program);
	});

	it('drops a title referenced through a separate statement', () => {
		const program = `root = Card([
  heading,
  PieChart("", "workspace", "", 1000, "count", "", 8)
])
heading = Title("Unassigned items")`;

		const stripped = stripDuplicateTitle(program, 'Unassigned items');

		expect(stripped).not.toContain('Title(');
		expect(renderedTitles(stripped)).toEqual([]);
	});

	it('keeps the title when it is the only child, so the tile is not empty', () => {
		const program = `root = Card([Title("Unassigned items")])`;

		expect(stripDuplicateTitle(program, 'Unassigned items')).toBe(program);
	});

	it('does nothing without a tile heading or a root statement', () => {
		expect(stripDuplicateTitle(withTitle, '')).toBe(withTitle);
		expect(stripDuplicateTitle('', 'Unassigned items')).toBe('');
		expect(stripDuplicateTitle('Title("Unassigned items")', 'Unassigned items')).toBe(
			'Title("Unassigned items")'
		);
	});

	it('leaves a widget that labels several blocks alone', () => {
		const program = `root = Card([
  Title("Open work"),
  Metric("Open", "status_completed = false"),
  Section("Recently updated", [ItemsTable("", "key,title")])
])`;
		const stripped = stripDuplicateTitle(program, 'Open work');

		// Only the leading duplicate goes; the section heading and the metrics
		// that followed it are untouched.
		expect(renderedTitles(stripped)).toEqual([]);
		expect(stripped).toContain('Section("Recently updated"');
		expect(stripped).toContain('Metric("Open"');
	});
});
