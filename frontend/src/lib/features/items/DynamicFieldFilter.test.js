import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeAll, describe, expect, test, vi } from 'vitest';
import { customFields } from '../../api/configuration.js';
import { queryLanguage } from '../../api/queryLanguage.js';


beforeAll(() => {
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {};
  }
});

const { customFieldsGetAll, getCatalog, getValues } = vi.hoisted(() => {
  const customFieldsGetAll = vi.fn(async () => []);
  const getCatalog = vi.fn(async () => ({
  fields: [
    {
      name: 'key',
      label: 'key',
      aliases: [],
      value_type: 'string',
      operators: ['=', '!=', '~', 'IN', 'NOT IN', 'IS NULL', 'IS NOT NULL'],
    },
    {
      name: 'id',
      label: 'id',
      aliases: [],
      value_type: 'number',
      operators: ['=', '!=', '<', '<=', '>', '>=', 'IS NULL', 'IS NOT NULL'],
    },
    {
      name: 'parent',
      label: 'parent',
      aliases: ['parentId', 'parent_id'],
      value_type: 'number',
      operators: ['=', '!=', 'IN', 'NOT IN', 'IS NULL', 'IS NOT NULL'],
    },
    {
      name: 'label',
      label: 'label',
      aliases: ['labels'],
      value_type: 'string',
      operators: ['=', '!=', 'IN', 'NOT IN'],
      value_help: { source: 'labels', value_field: 'name' },
    },
    {
      name: 'assignee',
      label: 'assignee',
      aliases: ['assignee'],
      value_type: 'number',
      operators: ['=', '!=', 'IN', 'NOT IN', 'IS NULL', 'IS NOT NULL'],
      value_help: { source: 'users', value_field: 'id' },
    },
    {
      name: 'cfid_44',
      label: 'Approver groups',
      aliases: ['cf_Approver groups', 'custom.Approver groups'],
      field_type: 'multiselect',
      value_type: 'string',
      operators: ['=', '!=', 'IN', 'NOT IN', 'IS NULL', 'IS NOT NULL'],
      values: [
        { value: 1, label: 'Developers' },
        { value: 2, label: 'Operations approvers' },
      ],
    },
    {
      name: 'cfid_45',
      label: 'Risk level',
      aliases: ['cf_Risk level', 'custom.Risk level'],
      field_type: 'select',
      value_type: 'string',
      operators: ['=', '!=', 'IN', 'NOT IN', 'IS NULL', 'IS NOT NULL'],
      values: [
        { value: 1, label: 'Low' },
        { value: 2, label: 'High' },
      ],
    },
  ],
}));
  const getValues = vi.fn(async (valueHelp, query) => {
  if (valueHelp?.source === 'users') {
    return [
      { value: 7, label: 'John Doe', description: 'jdoe' },
      { value: 8, label: 'John Doe', description: 'john.doe' },
    ];
  }
  return query === 'front' ? [{ value: 'Frontend', label: 'Frontend' }] : [];
});

  return { customFieldsGetAll, getCatalog, getValues };
});

vi.mock('../../api/configuration.js', () => ({
  customFields: { getAll: customFieldsGetAll },
}))

vi.mock('../../api/queryLanguage.js', () => ({
  queryLanguage: { getCatalog, getValues },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

const { default: DynamicFieldFilter } = await import('./DynamicFieldFilter.svelte');

afterEach(() => {
  cleanup();
  customFieldsGetAll.mockClear();
  getCatalog.mockClear();
  getValues.mockClear();
});

describe('DynamicFieldFilter value help', () => {
  test('searches centralized value help for a standard builder field', async () => {
    const onchange = vi.fn();
    render(DynamicFieldFilter, {
      props: {
        filter: {
          field: { id: 'labels', name: 'Labels', type: 'enum' },
          operator: '=',
          value: '',
          values: [],
        },
        testIdPrefix: 'labels-filter',
        onchange,
      },
    });

    const search = await screen.findByTestId('labels-filter-value-search');
    await fireEvent.click(search);
    await fireEvent.input(search, { target: { value: 'front' } });

    await waitFor(() =>
      expect(getValues).toHaveBeenCalledWith({ source: 'labels', value_field: 'name' }, 'front')
    );
    await fireEvent.click(await screen.findByTestId('labels-filter-value-option-Frontend'));
    expect(onchange).toHaveBeenLastCalledWith(expect.objectContaining({ value: 'Frontend' }));
  });

  test('searches and selects a custom multiselect option for equality', async () => {
    const onchange = vi.fn();
    render(DynamicFieldFilter, {
      props: {
        filter: {
          field: {
            id: 'cf_Approver groups',
            customFieldId: 44,
            name: 'Approver groups',
            type: 'multiselect',
            isCustom: true,
          },
          operator: '=',
          value: '',
          values: [],
        },
        testIdPrefix: 'approver-groups-filter',
        onchange,
      },
    });

    const search = await screen.findByTestId('approver-groups-filter-value-search');
    await fireEvent.click(search);
    await fireEvent.input(search, { target: { value: 'operations' } });
    await fireEvent.click(await screen.findByTestId('approver-groups-filter-value-option-2'));

    expect(onchange).toHaveBeenLastCalledWith(expect.objectContaining({ value: 2, values: [] }));
    expect(search).toHaveValue('Operations approvers');
    expect(getCatalog).toHaveBeenCalled();
    expect(customFieldsGetAll).not.toHaveBeenCalled();
  });

  test('keeps custom multiselect options searchable for IN', async () => {
    const onchange = vi.fn();
    render(DynamicFieldFilter, {
      props: {
        filter: {
          field: {
            id: 'cf_Approver groups',
            customFieldId: 44,
            name: 'Approver groups',
            type: 'multiselect',
            isCustom: true,
          },
          operator: 'IN',
          value: '',
          values: [],
        },
        testIdPrefix: 'approver-groups-multi-filter',
        onchange,
      },
    });

    const search = await screen.findByTestId('approver-groups-multi-filter-value-search');
    await fireEvent.click(search);
    await fireEvent.input(search, { target: { value: 'developers' } });
    await fireEvent.click(await screen.findByTestId('approver-groups-multi-filter-value-option-1'));

    await waitFor(() =>
      expect(onchange).toHaveBeenLastCalledWith(expect.objectContaining({ value: '', values: [1] }))
    );
  });

  test('offers catalog-only item fields such as Key in the filter picker', async () => {
    render(DynamicFieldFilter, {
      props: {
        filter: { field: null, operator: '=', value: '', values: [] },
        testIdPrefix: 'catalog-filter',
      },
    });

    await fireEvent.click(await screen.findByTestId('field-selector-trigger'));

    // These fields exist only in the backend QL catalog, not the curated
    // FieldSelector list (GH #328).
    expect(await screen.findByTestId('field-option-key')).toBeInTheDocument();
    expect(screen.getByTestId('field-option-id')).toBeInTheDocument();
    expect(screen.getByTestId('field-option-parent')).toBeInTheDocument();
  });

  test('shows the username on duplicate user value options', async () => {
    render(DynamicFieldFilter, {
      props: {
        filter: {
          field: { id: 'assignee', name: 'Assignee', type: 'user' },
          operator: '=',
          value: '',
          values: [],
        },
        testIdPrefix: 'assignee-filter',
        onchange: vi.fn(),
      },
    });

    const search = await screen.findByTestId('assignee-filter-value-search');
    await fireEvent.click(search);

    const first = await screen.findByTestId('assignee-filter-value-option-7');
    expect(first).toHaveTextContent('John Doe');
    expect(first).toHaveTextContent('jdoe');
    expect(screen.getByTestId('assignee-filter-value-option-8')).toHaveTextContent('john.doe');
  });

  test('searches and selects a custom single-select option', async () => {
    const onchange = vi.fn();
    render(DynamicFieldFilter, {
      props: {
        filter: {
          field: {
            id: 'cf_Risk level',
            customFieldId: 45,
            name: 'Risk level',
            type: 'select',
            isCustom: true,
          },
          operator: '=',
          value: '',
          values: [],
        },
        testIdPrefix: 'risk-level-filter',
        onchange,
      },
    });

    const search = await screen.findByTestId('risk-level-filter-value-search');
    await fireEvent.click(search);
    await fireEvent.input(search, { target: { value: 'high' } });
    await fireEvent.click(await screen.findByTestId('risk-level-filter-value-option-2'));

    expect(onchange).toHaveBeenLastCalledWith(expect.objectContaining({ value: 2, values: [] }));
  });
});
