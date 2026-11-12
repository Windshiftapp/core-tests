import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api/queryLanguage.js', () => ({
  queryLanguage: {
    getCatalog: vi.fn(async () => ({ fields: [] })),
    getValues: vi.fn(async () => []),
  },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({ t: (key) => key }));

const { default: FilterBuilderModal } = await import('./FilterBuilderModal.svelte');

afterEach(cleanup);

const NULL_ROW = {
  field: { id: 'cf_Risk level', name: 'Risk level', type: 'select' },
  operator: 'IS NULL',
  value: '',
  values: [],
};

describe('FilterBuilderModal', () => {
  it('applies a filter whose only row uses a null operator', async () => {
    const onApply = vi.fn();
    render(FilterBuilderModal, {
      props: { isOpen: true, initialRows: [NULL_ROW], onApply },
    });

    const apply = await screen.findByTestId('dynamic-filter-apply');
    expect(apply).toBeEnabled();
    await fireEvent.click(apply);

    expect(onApply).toHaveBeenCalledWith([NULL_ROW]);
  });

  it('keeps apply disabled for a row that still needs a value', async () => {
    render(FilterBuilderModal, {
      props: {
        isOpen: true,
        initialRows: [{ ...NULL_ROW, operator: '=' }],
      },
    });

    expect(await screen.findByTestId('dynamic-filter-apply')).toBeDisabled();
  });
});
