import { describe, expect, it } from 'vitest';

import { getDashboardWidgetTitle } from './dashboardWidgetRegistry.js';

const translate = (key) => key;

describe('getDashboardWidgetTitle', () => {
  it('uses the author-given name for a custom widget', () => {
    expect(
      getDashboardWidgetTitle(
        { type: 'custom-widget', config: { customWidgetId: 4, title: 'Unassigned over 30 days' } },
        translate
      )
    ).toBe('Unassigned over 30 days');
  });

  it('falls back to the catalog label when a custom widget has no cached name', () => {
    expect(getDashboardWidgetTitle({ type: 'custom-widget', config: { customWidgetId: 4 } }, translate)).toBe(
      'dashboard.widgetCatalog.customWidget.name'
    );
  });

  it('uses the catalog label for built-in widgets', () => {
    expect(getDashboardWidgetTitle({ type: 'daily-briefing', config: {} }, translate)).toBe(
      'dashboard.widgetCatalog.dailyBriefing.name'
    );
  });

  it('falls back to the raw type for an unknown widget', () => {
    expect(getDashboardWidgetTitle({ type: 'mystery', config: {} }, translate)).toBe('mystery');
  });
});
