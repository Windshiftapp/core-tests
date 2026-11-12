import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, test, vi } from 'vitest';

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
  i18n: { locale: 'en-US' },
}));

import CardFieldChip from './CardFieldChip.svelte';

afterEach(() => cleanup());

describe('CardFieldChip custom fields', () => {
  test('renders every selected multiselect option in stored order', () => {
    render(CardFieldChip, {
      props: {
        cardField: { field_type: 'custom', field_identifier: 'custom_field_42' },
        item: { custom_field_values: { 42: [1, 3, 2] } },
        customFieldDefinitions: [{
          id: 42,
          name: 'Impact',
          field_type: 'multiselect',
          options: JSON.stringify({
            next_id: 4,
            items: [
              { id: 1, label: 'Low' },
              { id: 2, label: 'Medium' },
              { id: 3, label: 'High' },
            ],
          }),
        }],
      },
    });

    expect(screen.getByText('Low, High, Medium')).toBeInTheDocument();
  });

  test('falls back to the stored value when a number is corrupt', () => {
	render(CardFieldChip, {
	  props: {
		cardField: { field_type: 'custom', field_identifier: 'custom_field_43' },
		item: { custom_field_values: { 43: 'not-a-number' } },
		customFieldDefinitions: [
		  { id: 43, name: 'Estimate', field_type: 'number' },
		],
	  },
	});

	expect(screen.getByText('not-a-number')).toBeInTheDocument();
	expect(screen.queryByText('NaN')).not.toBeInTheDocument();
  });
});

describe('CardFieldChip parent field', () => {
  test('renders the permission-checked parent key', () => {
    render(CardFieldChip, {
      props: {
        cardField: { field_type: 'system', field_identifier: 'parent' },
        item: {
          id: 42,
          parent_id: 7,
          parent_key: 'EPIC-7',
          parent_title: 'Release the new workflow',
          workspace_key: 'CHILD',
        },
      },
    });

    const chip = screen.getByTestId('work-item-parent-42');
    expect(chip).toHaveTextContent('EPIC-7');
    expect(chip).not.toHaveTextContent('CHILD-7');
  });

  test('renders nothing for a masked parent', () => {
    render(CardFieldChip, {
      props: {
        cardField: { field_type: 'system', field_identifier: 'parent' },
        item: {
          id: 42,
          parent_id: 7,
          parent_key: '',
          parent_title: '',
          parent_workspace_item_number: null,
          workspace_key: 'CHILD',
        },
      },
    });

    expect(screen.queryByTestId('work-item-parent-42')).not.toBeInTheDocument();
  });
});
