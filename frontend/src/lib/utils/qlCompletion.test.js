import { describe, expect, test } from 'vitest';
import { applyQlSuggestion, buildQlSuggestions, completionValues, getQlCompletionContext } from './qlCompletion.js';

const catalog = {
  logical_operators: ['AND', 'OR'],
  fields: [
    {
      name: 'milestone',
      label: 'milestone',
      aliases: ['milestoneName'],
      value_type: 'string',
      operators: ['=', '!=', 'IN', 'NOT IN'],
      value_help: {
        source: 'milestones',
        value_field: 'name',
      },
    },
    {
      name: 'status',
      label: 'status',
      aliases: [],
      value_type: 'string',
      operators: ['=', '!='],
    },
    {
      name: 'cfid_5',
      label: 'Customer impact',
      aliases: ['cf_Customer impact'],
      value_type: 'string',
      operators: ['='],
    },
  ],
};

describe('QL completion', () => {
  test('offers every matching field without changing the query', () => {
    const query = 'mile';
    const context = getQlCompletionContext(query, query.length, catalog);
    const suggestions = buildQlSuggestions(context, catalog);

    expect(context).toMatchObject({ kind: 'field', fragment: 'mile', start: 0, end: 4 });
    expect(suggestions.map((suggestion) => suggestion.value)).toEqual(['milestone']);
    expect(query).toBe('mile');
  });

  test('offers all milestone values after equals and inserts only the selected value', () => {
    const query = 'status = "Open" AND milestone = ';
    const context = getQlCompletionContext(query, query.length, catalog);
    const values = [
      { value: 'Q1', label: 'Q1' },
      { value: 'Q2', label: 'Q2' },
      { value: 'Launch', label: 'Launch' },
    ];
    const suggestions = buildQlSuggestions(context, catalog, values);

    expect(context).toMatchObject({ kind: 'value', fragment: '' });
    expect(suggestions.map((suggestion) => suggestion.value)).toEqual(['Q1', 'Q2', 'Launch']);

    const selected = applyQlSuggestion(query, context, suggestions[1]);
    expect(selected.query).toBe('status = "Open" AND milestone = "Q2"');
  });

  test('filters values inside an unfinished quoted literal', () => {
    const query = 'milestone = "la';
    const context = getQlCompletionContext(query, query.length, catalog);
    const suggestions = buildQlSuggestions(context, catalog, [
      { value: 'Launch', label: 'Launch' },
      { value: 'Q1', label: 'Q1' },
    ]);

    expect(context).toMatchObject({ kind: 'value', fragment: 'la' });
    expect(suggestions.map((suggestion) => suggestion.value)).toEqual(['Launch']);
    expect(applyQlSuggestion(query, context, suggestions[0]).query).toBe('milestone = "Launch"');
  });

  test('matches custom fields by their display label', () => {
    const query = 'impact';
    const context = getQlCompletionContext(query, query.length, catalog);
    const suggestions = buildQlSuggestions(context, catalog);

    expect(suggestions).toHaveLength(1);
    expect(suggestions[0]).toMatchObject({ value: 'cfid_5', label: 'Customer impact' });
  });

  test('normalizes centralized value-help rows', () => {
    const values = completionValues(
      [
        { value: 4, label: 'Q4' },
        { value: 5, label: 'Q5' },
      ],
      { value_field: 'id' }
    );

    expect(values).toEqual([
      { value: 4, label: 'Q4' },
      { value: 5, label: 'Q5' },
    ]);
  });
});
