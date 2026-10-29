import { cleanup, render } from '@testing-library/svelte';
import { afterEach, describe, expect, it } from 'vitest';

import DraggableFieldRow from './DraggableFieldRow.svelte';

afterEach(cleanup);

describe('DraggableFieldRow', () => {
  it('preserves drag selectors and renders the shared drag affordance', () => {
    const { container } = render(DraggableFieldRow, {
      fieldId: 'summary',
      fieldIndex: 2,
      closestEdge: 'top',
      attributes: { 'data-configured-field': true },
      handleAttributes: { 'data-testid': 'field-drag-summary' },
    });

    const row = container.querySelector('[data-configured-field]');
    expect(row).toHaveAttribute('data-field-id', 'summary');
    expect(row).toHaveAttribute('data-field-index', '2');
    expect(container.querySelector('[data-testid="field-drag-summary"]')).toBeTruthy();
    expect(container.querySelector('.drop-indicator')).toBeTruthy();
  });
});
