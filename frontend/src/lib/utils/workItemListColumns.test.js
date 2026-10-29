import { describe, expect, test } from 'vitest';
import { DEFAULT_LIST_COLUMNS, listGridMinWidth, listGridTemplateColumns } from './workItemListColumns.js';
import { LIST_COLUMN_FIELDS } from '../stores/fieldConfig.js';

// Start and end dates are selectable list/tree columns alongside the due date.
describe('start and end date list columns', () => {
  test('exposes start_date and end_date as optional columns', () => {
    for (const identifier of ['start_date', 'end_date']) {
      const field = LIST_COLUMN_FIELDS.find((item) => item.identifier === identifier);
      expect(field, `${identifier} must be a list column field`).toBeDefined();
      expect(field.listColumn).toEqual({ required: false });
    }
  });

  test('gives the date columns fixed tracks so headers and rows stay aligned', () => {
    const template = listGridTemplateColumns([
      { field_identifier: 'key' },
      { field_identifier: 'title' },
      { field_identifier: 'start_date', width: 2 },
      { field_identifier: 'end_date', width: 2 },
    ]);
    expect(template).not.toContain('max-content');
    expect(template).not.toMatch(/(^|\s)auto(\s|$)/);
  });
});

// The list header and each row build their own grid from this template. Any
// content-sized track (`max-content` / `auto`) resolves differently per grid,
// so the header labels drift out of line with the row cells. Every track must
// be content-independent.
describe('listGridTemplateColumns', () => {
  const columns = [
    { field_identifier: 'key', width: 2 },
    { field_identifier: 'title', width: 4 },
    { field_identifier: 'due_date', width: 2 },
    { field_identifier: 'story_points', width: 2 },
    { field_identifier: 'iteration', width: 2 },
  ];

  test('emits no content-sized tracks', () => {
    const template = listGridTemplateColumns(columns);
    expect(template).not.toContain('max-content');
    expect(template).not.toMatch(/(^|\s)auto(\s|$)/);
  });

  test('fixes the key and actions tracks regardless of column content', () => {
    const tracks = listGridTemplateColumns(columns).split(' ');
    expect(tracks[0]).toBe('5rem');
    expect(tracks.at(-1)).toBe('4rem');
  });

  test('keeps the default column set deterministic', () => {
    const template = listGridTemplateColumns(DEFAULT_LIST_COLUMNS);
    expect(template).not.toContain('max-content');
    expect(template).not.toMatch(/(^|\s)auto(\s|$)/);
  });
});

describe('listGridMinWidth', () => {
  test('reserves the fixed key and actions tracks plus every gap', () => {
    const columns = [
      { field_identifier: 'key', width: 2 },
      { field_identifier: 'title', width: 4 },
      { field_identifier: 'due_date', width: 2 },
      { field_identifier: 'story_points', width: 2 },
      { field_identifier: 'iteration', width: 2 },
    ];
    // key 5 + title 16 + due_date 5.25 + story_points 10 + iteration 6.75
    // + actions 4 + 5 gaps = 52rem
    expect(listGridMinWidth(columns)).toBe('52rem');
  });
});
