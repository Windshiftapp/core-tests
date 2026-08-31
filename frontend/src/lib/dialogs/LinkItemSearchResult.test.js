import { render, screen } from '@testing-library/svelte';
import { describe, expect, test } from 'vitest';

import LinkItemSearchResult from './LinkItemSearchResult.svelte';

describe('LinkItemSearchResult', () => {
  test('renders a work item with its canonical key and no internal metadata badge', () => {
    render(LinkItemSearchResult, {
      props: {
        highlighted: true,
        result: {
          id: 927,
          type: 'item',
          title: 'Picker result',
          workspace_name: 'Windshift',
          workspace_key: 'WI',
          workspace_item_number: 657,
          item_type_icon: 'Bug',
          item_type_color: '#e5484d',
        },
      },
    });

    const result = screen.getByTestId('link-search-result');
    expect(result).toHaveTextContent('Picker result');
    expect(result).toHaveTextContent('WI-657');
    expect(result).not.toHaveTextContent('Windshift');
    expect(result).not.toHaveTextContent('ID 927');
    expect(result).not.toHaveTextContent('Work Item');
    expect(result.getAttribute('style')).toContain('var(--ds-surface-raised-hovered)');
  });
});
