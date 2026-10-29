import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { queryLanguage } from '../../api/queryLanguage.js';

const catalog = {
  logical_operators: ['AND'],
  fields: [
    {
      name: 'cfid_1',
      label: 'Priority score',
      field_type: 'number',
      aliases: ['cf_priority_score'],
      operators: ['='],
    },
  ],
};

vi.mock('../../api/queryLanguage.js', () => ({
  queryLanguage: {
        getCatalog: vi.fn(async () => catalog),
        getValues: vi.fn(async () => []),
      },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

const { default: QlFilterBuilder } = await import('./QlFilterBuilder.svelte');

afterEach(cleanup);

const completeFilter = {
  field: { id: 'cf_priority_score', customFieldId: 1, type: 'number', name: 'Priority score' },
  operator: '=',
  value: '3',
  values: [],
};

describe('QlFilterBuilder', () => {
  test('emits the built QL and clears filter_state when switching to raw mode', async () => {
    const onchange = vi.fn();
    render(QlFilterBuilder, {
      props: {
        filterState: JSON.stringify({ dynamicFields: [completeFilter] }),
        completionCatalog: catalog,
        onchange,
      },
    });

    await waitFor(() => expect(screen.getByTestId('ql-filter-builder-row-0')).toBeInTheDocument());
    await fireEvent.click(screen.getByTestId('ql-filter-builder-mode-raw'));

    const last = onchange.mock.calls.at(-1)[0];
    expect(last.mode).toBe('raw');
    expect(last.ql_query).toBe('`cf_priority_score` = 3');
    expect(last.filter_state).toBeNull();
  });

  test('hydrates a raw custom-field QL back into builder rows', async () => {
    render(QlFilterBuilder, {
      props: {
        qlQuery: 'cf_priority_score = 3',
        completionCatalog: catalog,
      },
    });

    await waitFor(() => expect(screen.getByTestId('ql-filter-builder-row-0')).toBeInTheDocument());
    expect(screen.getByTestId('ql-filter-builder-mode-builder')).toHaveAttribute(
      'aria-selected',
      'true'
    );
  });

  test('keeps raw mode and warns when the QL cannot be rebuilt', async () => {
    render(QlFilterBuilder, {
      props: {
        qlQuery: 'statusCompleted = false',
        completionCatalog: catalog,
      },
    });

    await waitFor(() => expect(screen.getByTestId('ql-filter-builder-raw')).toBeInTheDocument());
    expect(screen.getByTestId('ql-filter-builder-mode-raw')).toHaveAttribute(
      'aria-selected',
      'true'
    );
    expect(screen.getByTestId('ql-filter-builder-hydration-failed')).toBeInTheDocument();
  });

  test('emits the remaining filter_state when a row is removed', async () => {
    const onchange = vi.fn();
    render(QlFilterBuilder, {
      props: {
        filterState: JSON.stringify({ dynamicFields: [completeFilter] }),
        completionCatalog: catalog,
        onchange,
      },
    });

    await waitFor(() =>
      expect(screen.getByTestId('ql-filter-builder-remove-0')).toBeInTheDocument()
    );
    await fireEvent.click(screen.getByTestId('ql-filter-builder-remove-0'));

    const last = onchange.mock.calls.at(-1)[0];
    expect(last.mode).toBe('builder');
    expect(last.ql_query).toBe('');
    expect(JSON.parse(last.filter_state)).toEqual({ dynamicFields: [] });
  });

  test('adds an empty builder row and emits the new filter_state', async () => {
    const onchange = vi.fn();
    render(QlFilterBuilder, {
      props: { completionCatalog: catalog, onchange },
    });

    await fireEvent.click(screen.getByTestId('ql-filter-builder-add'));
    expect(screen.getByTestId('ql-filter-builder-row-0')).toBeInTheDocument();
    const last = onchange.mock.calls.at(-1)[0];
    expect(JSON.parse(last.filter_state).dynamicFields).toHaveLength(1);
  });
});
