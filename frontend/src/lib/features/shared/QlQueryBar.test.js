import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, test, vi } from 'vitest';


const { completionCatalog, getCatalog, getValues } = vi.hoisted(() => {
  const completionCatalog = {
  logical_operators: ['AND', 'OR'],
  fields: [
    {
      name: 'milestone',
      label: 'milestone',
      aliases: ['milestoneName'],
      value_type: 'string',
      operators: ['=', '!='],
      value_help: {
        source: 'milestones',
        value_field: 'name',
      },
    },
    {
      name: 'label',
      label: 'label',
      aliases: ['labels'],
      value_type: 'string',
      operators: ['=', '!='],
      value_help: {
        source: 'labels',
        value_field: 'name',
      },
    },
  ],
};

  const getCatalog = vi.fn(async () => completionCatalog);
  const getValues = vi.fn(async (valueHelp) =>
  valueHelp.source === 'labels'
    ? [{ value: 'Frontend', label: 'Frontend' }]
    : [
        { value: 'Q1 2027', label: 'Q1 2027' },
        { value: 'Launch', label: 'Launch' },
      ]
);

  return { completionCatalog, getCatalog, getValues };
});

vi.mock('../../api/queryLanguage.js', () => ({
  queryLanguage: { getCatalog, getValues },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

const { default: QlQueryBar } = await import('./QlQueryBar.svelte');

afterEach(() => {
  cleanup();
  getCatalog.mockClear();
  getValues.mockClear();
});

describe('QlQueryBar completion', () => {
  test('shows matching fields and applies one only when selected', async () => {
    const onquerychange = vi.fn();
    render(QlQueryBar, {
      props: { query: 'mile', mode: 'raw', onquerychange },
    });

    await fireEvent.focus(screen.getByTestId('ql-editor'));
    const suggestion = await screen.findByTestId('ql-suggestion-field-milestone');
    expect(onquerychange).not.toHaveBeenCalled();

    await fireEvent.click(suggestion);
    expect(onquerychange).toHaveBeenCalledWith('milestone ');
  });

  test('shows every milestone value after equals', async () => {
    const onquerychange = vi.fn();
    render(QlQueryBar, {
      props: { query: 'milestone = ', mode: 'raw', onquerychange },
    });

    await fireEvent.focus(screen.getByTestId('ql-editor'));
    await waitFor(() => expect(getValues).toHaveBeenCalledOnce());
    expect(await screen.findByTestId('ql-suggestion-value-q1-2027')).toBeInTheDocument();
    expect(screen.getByTestId('ql-suggestion-value-launch')).toBeInTheDocument();

    await fireEvent.click(screen.getByTestId('ql-suggestion-value-q1-2027'));
    expect(onquerychange).toHaveBeenCalledWith('milestone = "Q1 2027"');
  });

  test('requests matching visible labels from centralized value help', async () => {
    render(QlQueryBar, {
      props: { query: 'label = "front', mode: 'raw' },
    });

    await fireEvent.focus(screen.getByTestId('ql-editor'));
    await waitFor(() =>
      expect(getValues).toHaveBeenCalledWith(expect.objectContaining({ source: 'labels' }), 'front')
    );
    expect(await screen.findByTestId('ql-suggestion-value-frontend')).toBeInTheDocument();
  });
});

test('uses the supplied asset catalog and completes a status without item requests', async () => {
  const onquerychange = vi.fn();
  render(QlQueryBar, {
    props: {
      query: 'status = ',
      mode: 'raw',
      compact: true,
      onquerychange,
      completionCatalog: {
        logical_operators: ['AND', 'OR'],
        fields: [
          {
            name: 'status',
            value_type: 'string',
            operators: ['=', '!='],
            values: [{ value: 'In service', label: 'In service' }],
          },
        ],
      },
    },
  });
  await fireEvent.focus(screen.getByTestId('ql-editor'));
  await screen.findByTestId('ql-suggestion-value-in-service');
  await fireEvent.keyDown(screen.getByTestId('ql-editor'), { key: 'Tab' });
  expect(onquerychange).toHaveBeenCalledWith('status = "In service"');
  expect(getCatalog).not.toHaveBeenCalled();
  expect(getValues).not.toHaveBeenCalled();
});

test('executes a compact query on Enter when suggestions are dismissed', async () => {
  const onexecute = vi.fn();
  render(QlQueryBar, {
    props: {
      query: 'title ~ "server"',
      mode: 'raw',
      compact: true,
      onexecute,
      completionCatalog: { fields: [], logical_operators: [] },
    },
  });
  await fireEvent.keyDown(screen.getByTestId('ql-editor'), { key: 'Enter' });
  expect(onexecute).toHaveBeenCalledOnce();
});
