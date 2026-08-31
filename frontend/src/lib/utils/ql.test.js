import { describe, expect, test } from 'vitest';
import { QLBuilder, QLEvaluator } from './ql.js';

describe('QLBuilder.tryParseToBuilder', () => {
  test('returns null on empty input', () => {
    expect(QLBuilder.tryParseToBuilder('')).toBeNull();
    expect(QLBuilder.tryParseToBuilder('   ')).toBeNull();
  });

  test('recognizes workspace, status, priority, and title clauses', () => {
    const r = QLBuilder.tryParseToBuilder(
      'workspaceKey = "ALPHA" AND status_id IN (1, 2) AND priority_id = 3 AND title ~ "foo"'
    );
    expect(r).toEqual({
      workspaces: ['ALPHA'],
      statuses: [1, 2],
      priorities: [3],
      search: 'foo',
      dynamicFields: [],
      dropped: false,
    });
  });

  test('builds workspace filters with stable workspace keys', () => {
    expect(
      QLBuilder.buildQuery({
        workspaces: ['WI', 'OPS'],
        statuses: [],
        priorities: [],
        search: '',
        dynamicFields: [],
      })
    ).toBe('workspaceKey IN ("WI", "OPS")');
  });

  test.each([
    ['workspace = "WI"', ['WI']],
    ['workspace = WI', ['WI']],
    ['workspace IN ("WI", "OPS")', ['WI', 'OPS']],
    ['workspace IN (WI, OPS)', ['WI', 'OPS']],
    ['workspaceKey = WI', ['WI']],
  ])('recovers workspace literals through the shared parser: %s', (query, workspaces) => {
    expect(QLBuilder.tryParseToBuilder(query)).toEqual({
      workspaces,
      statuses: [],
      priorities: [],
      search: '',
      dynamicFields: [],
      dropped: false,
    });
  });

  test('recovers a backticked custom-field equality clause as a dynamicField', () => {
    const r = QLBuilder.tryParseToBuilder('`cf_stage` = "draft"');
    expect(r.dynamicFields).toEqual([
      {
        field: { id: 'cf_stage', type: 'text', name: 'cf_stage' },
        operator: '=',
        value: 'draft',
        values: [],
      },
    ]);
    expect(r.dropped).toBe(false);
  });

  test('uses provided custom-field catalog to resolve type and label', () => {
    const r = QLBuilder.tryParseToBuilder('`cf_priority` = 5', {
      customFields: [{ id: 'cf_priority', type: 'number', name: 'Priority Score' }],
    });
    expect(r.dynamicFields[0].field).toEqual({
      id: 'cf_priority',
      type: 'number',
      name: 'Priority Score',
    });
    expect(r.dynamicFields[0].operator).toBe('=');
    expect(r.dynamicFields[0].value).toBe('5');
  });

  test('recovers IN list with quoted values', () => {
    const r = QLBuilder.tryParseToBuilder('`cf_stage` IN ("draft", "review")');
    expect(r.dynamicFields[0]).toMatchObject({
      operator: 'IN',
      values: ['draft', 'review'],
    });
  });

  test('recovers NOT IN with numeric values and infers number type', () => {
    const r = QLBuilder.tryParseToBuilder('`cf_score` NOT IN (1, 2, 3)');
    expect(r.dynamicFields[0]).toMatchObject({
      operator: 'NOT IN',
      values: ['1', '2', '3'],
      field: { type: 'number' },
    });
  });

  test('recovers ~ contains operator on a custom field', () => {
    const r = QLBuilder.tryParseToBuilder('`cf_notes` ~ "urgent"');
    expect(r.dynamicFields[0]).toMatchObject({
      operator: '~',
      value: 'urgent',
      field: { type: 'text' },
    });
  });

  test('recovers boolean inferred type from bare true/false', () => {
    const r = QLBuilder.tryParseToBuilder('`cf_done` = true');
    expect(r.dynamicFields[0]).toMatchObject({
      operator: '=',
      value: 'true',
      field: { type: 'boolean' },
    });
  });

  test.each([
    ['`cf_due` IS NULL', 'IS NULL'],
    ['`cf_due` IS NOT NULL', 'IS NOT NULL'],
  ])('round-trips a null check: %s', (query, operator) => {
    const parsed = QLBuilder.tryParseToBuilder(query, {
      customFields: [{ id: 'cf_due', type: 'date', name: 'Due' }],
    });
    expect(parsed?.dynamicFields).toEqual([
      {
        field: { id: 'cf_due', type: 'date', name: 'Due' },
        operator,
        value: '',
        values: [],
      },
    ]);
    expect(parsed?.dropped).toBe(false);
    expect(QLBuilder.buildQuery(parsed)).toBe(query);
  });

  test('builds null checks without requiring a value', () => {
    expect(
      QLBuilder.buildFieldCondition({
        field: { id: 'cf_owner', type: 'user' },
        operator: 'IS NOT NULL',
        value: '',
        values: [],
      })
    ).toBe('`cf_owner` IS NOT NULL');
  });

  test('combines a built-in clause with a custom-field clause', () => {
    const r = QLBuilder.tryParseToBuilder('`cf_stage` = "draft" AND status_id = 5');
    expect(r.statuses).toEqual([5]);
    expect(r.dynamicFields[0]).toMatchObject({
      operator: '=',
      value: 'draft',
    });
    expect(r.dropped).toBe(false);
  });

  test('flags dropped=true when NOT or OR clauses remain unparsed', () => {
    const r = QLBuilder.tryParseToBuilder('NOT (`cf_stage` = "draft") OR `cf_other` = "x"');
    expect(r.dropped).toBe(true);
  });

  test('formatValue quotes non-numeric string values for user/reference fields', () => {
    // Number.isNaN('group-foo') is false, but it's still a string — make sure
    // formatValue quotes it rather than emitting it bare as an identifier.
    expect(QLBuilder.formatValue('group-foo', 'user')).toBe('"group-foo"');
    expect(QLBuilder.formatValue('group-foo', 'reference')).toBe('"group-foo"');
  });

  test('formatValue leaves numeric ID values unquoted for user/reference fields', () => {
    expect(QLBuilder.formatValue(42, 'user')).toBe('42');
    // String form of a number also passes through unquoted (UI sometimes emits
    // IDs as strings from picker components).
    expect(QLBuilder.formatValue('42', 'reference')).toBe('42');
  });

  test('formatValue emits bare null for null/undefined', () => {
    expect(QLBuilder.formatValue(null, 'text')).toBe('null');
    expect(QLBuilder.formatValue(undefined, 'number')).toBe('null');
  });

  test('round-trips a builder query back through buildQuery', () => {
    const original = QLBuilder.buildQuery({
      statuses: [3],
      priorities: [],
      workspaces: [],
      search: '',
      dynamicFields: [
        {
          field: { id: 'cf_stage', type: 'text', name: 'Stage' },
          operator: '=',
          value: 'draft',
          values: [],
        },
      ],
    });
    const parsed = QLBuilder.tryParseToBuilder(original, {
      customFields: [{ id: 'cf_stage', type: 'text', name: 'Stage' }],
    });
    expect(parsed.statuses).toEqual([3]);
    expect(parsed.dynamicFields).toEqual([
      {
        field: { id: 'cf_stage', type: 'text', name: 'Stage' },
        operator: '=',
        value: 'draft',
        values: [],
      },
    ]);
    expect(parsed.dropped).toBe(false);
  });

  test('recovers name-based status / priority clauses via catalogs', () => {
    const r = QLBuilder.tryParseToBuilder('status = "Open" AND priority IN ("High", "Low")', {
      statuses: [
        { id: 1, name: 'Open' },
        { id: 2, name: 'Closed' },
      ],
      priorities: [
        { id: 5, name: 'High' },
        { id: 6, name: 'Low' },
      ],
    });
    expect(r.statuses).toEqual([1]);
    expect(r.priorities).toEqual([5, 6]);
    expect(r.dropped).toBe(false);
  });

  test('does not confuse status_id with the name-based status clause', () => {
    const r = QLBuilder.tryParseToBuilder('status_id = 3', {
      statuses: [{ id: 1, name: 'Open' }],
    });
    expect(r.statuses).toEqual([3]);
    expect(r.dropped).toBe(false);
  });

  test('marks dropped when a name-based clause cannot be resolved', () => {
    const r = QLBuilder.tryParseToBuilder('workspace = "alpha" AND status = "Nope"', {
      statuses: [{ id: 1, name: 'Open' }],
    });
    expect(r.dropped).toBe(true);
  });
});

describe('QLEvaluator workspace references', () => {
  test.each([
    'workspace = WI',
    'workspace = "WI"',
    'workspace IN (WI)',
    'workspace IN ("WI")',
    'workspace = "Windshift"',
  ])('matches workspace names and keys with quoted or bare values: %s', (query) => {
    const evaluator = new QLEvaluator([
      { id: 7, key: 'WI', name: 'Windshift' },
      { id: 8, key: 'OPS', name: 'Operations' },
    ]);
    const items = [
      { id: 1, workspace_id: 7 },
      { id: 2, workspace_id: 8 },
    ];

    expect(evaluator.filter(items, query)).toEqual([{ id: 1, workspace_id: 7 }]);
  });
});
