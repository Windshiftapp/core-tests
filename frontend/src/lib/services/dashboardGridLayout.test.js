import { describe, expect, it } from 'vitest';
import { getDashboardResizeBounds, resizeDashboardWidgetRow } from './dashboardGridLayout.js';

const minimums = {
  primary: 3,
  secondary: 3,
  compact: 2,
};
const getMinWidth = (type) => minimums[type] ?? 3;

describe('dashboard row resizing', () => {
  it('transfers the width delta to the next widget and preserves 12 columns', () => {
    const widgets = [
      { id: 'left', type: 'primary', width: 6 },
      { id: 'right', type: 'secondary', width: 6 },
    ];

    const result = resizeDashboardWidgetRow(widgets, 'left', 8, getMinWidth);

    expect(result.width).toBe(8);
    expect(result.widgets.map(({ id, width }) => ({ id, width }))).toEqual([
      { id: 'left', width: 8 },
      { id: 'right', width: 4 },
    ]);
    expect(result.widgets.reduce((sum, widget) => sum + widget.width, 0)).toBe(12);
  });

  it('clamps expansion at the neighbour minimum', () => {
    const widgets = [
      { id: 'left', type: 'primary', width: 6 },
      { id: 'right', type: 'secondary', width: 6 },
    ];

    const bounds = getDashboardResizeBounds(widgets, 'left', getMinWidth);
    const result = resizeDashboardWidgetRow(widgets, 'left', 12, getMinWidth);

    expect(bounds).toMatchObject({
      minWidth: 3,
      maxWidth: 9,
      neighbourId: 'right',
    });
    expect(result.widgets.map((widget) => widget.width)).toEqual([9, 3]);
  });

  it('uses the preceding widget for the final widget in a row', () => {
    const widgets = [
      { id: 'left', type: 'primary', width: 9 },
      { id: 'right', type: 'secondary', width: 3 },
    ];

    const result = resizeDashboardWidgetRow(widgets, 'right', 6, getMinWidth);

    expect(result.widgets.map((widget) => widget.width)).toEqual([6, 6]);
  });

  it('lets a single widget resize into unused columns', () => {
    const widgets = [{ id: 'single', type: 'primary', width: 6 }];

    const bounds = getDashboardResizeBounds(widgets, 'single', getMinWidth);
    const result = resizeDashboardWidgetRow(widgets, 'single', 12, getMinWidth);

    expect(result.width).toBe(12);
    expect(result.widgets).toEqual([{ id: 'single', type: 'primary', width: 12 }]);
    expect(bounds).toMatchObject({
      minWidth: 3,
      maxWidth: 12,
      neighbourId: null,
    });
  });

  it('uses empty row space before shrinking a neighbour', () => {
    const widgets = [
      { id: 'left', type: 'compact', width: 4 },
      { id: 'right', type: 'compact', width: 4 },
    ];

    const result = resizeDashboardWidgetRow(widgets, 'left', 6, getMinWidth);

    expect(result.widgets.map((widget) => widget.width)).toEqual([6, 4]);
  });

  it('changes only one neighbour in a three-widget row', () => {
    const widgets = [
      { id: 'first', type: 'compact', width: 4 },
      { id: 'middle', type: 'compact', width: 4 },
      { id: 'last', type: 'secondary', width: 4 },
    ];

    const result = resizeDashboardWidgetRow(widgets, 'middle', 7, getMinWidth);

    expect(result.widgets.map((widget) => widget.width)).toEqual([4, 5, 3]);
    expect(result.widgets.reduce((sum, widget) => sum + widget.width, 0)).toBe(12);
  });
});
