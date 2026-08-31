import { render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it } from 'vitest';

import Chart from './Chart.svelte';

beforeEach(() => {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
});

describe('Chart line interpolation', () => {
  it('draws a continuous cubic curve without the old quadratic joins', () => {
    render(Chart, {
      props: {
        type: 'line',
        categories: ['One', 'Two', 'Three', 'Four'],
        series: [
          {
            key: 'completed',
            label: 'Completed',
            color: '#22a06b',
            values: [2, 9, 4, 8],
            showArea: false,
          },
        ],
      },
    });

    const path = screen.getByTestId('chart-series-completed').getAttribute('d');
    expect(path).toMatch(/^M [\d.]+ [\d.]+ C /);
    expect(path?.match(/ C /g)).toHaveLength(3);
    expect(path).not.toContain(' Q ');
  });
});
