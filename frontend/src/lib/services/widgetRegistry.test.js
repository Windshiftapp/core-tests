import { describe, expect, it } from 'vitest';

import {
  clampWidgetWidth,
  getDefaultWidth,
  getWidgetMaxWidth,
  getWidgetMinWidth,
  WORKSPACE_WIDGET_GRID_COLUMNS,
  widgetRegistry,
} from './widgetRegistry.js';

describe('workspace widget registry widths', () => {
  it('keeps every width constraint within the workspace grid', () => {
    expect(WORKSPACE_WIDGET_GRID_COLUMNS).toBe(3);

    for (const widget of widgetRegistry) {
      expect(widget.minWidth, `${widget.type} minWidth`).toBeGreaterThanOrEqual(1);
      expect(widget.defaultWidth, `${widget.type} defaultWidth`).toBeGreaterThanOrEqual(
        widget.minWidth
      );
      expect(widget.maxWidth, `${widget.type} maxWidth`).toBeGreaterThanOrEqual(
        widget.defaultWidth
      );
      expect(widget.maxWidth, `${widget.type} maxWidth`).toBeLessThanOrEqual(
        WORKSPACE_WIDGET_GRID_COLUMNS
      );
    }
  });

  it('exposes the registry bounds and clamps invalid widths', () => {
    expect(getWidgetMinWidth('created-chart')).toBe(1);
    expect(getDefaultWidth('created-chart')).toBe(1);
    expect(getWidgetMaxWidth('created-chart')).toBe(3);
    expect(clampWidgetWidth('created-chart', 12)).toBe(3);
    expect(clampWidgetWidth('created-chart', 0)).toBe(1);
    expect(clampWidgetWidth('created-chart', Number.NaN)).toBe(1);
  });
});
