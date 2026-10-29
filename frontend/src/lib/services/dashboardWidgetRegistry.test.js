import { describe, expect, test } from 'vitest';

import { buildDefaultDashboardLayout, dashboardWidgetCategories, dashboardWidgetRegistry, getDashboardWidgetDefaultWidth, getDashboardWidgetMinWidth, getDashboardWidgetsByCategory } from './dashboardWidgetRegistry.js';

describe('dashboardWidgetRegistry', () => {
  test('every widget has a 12-column defaultWidth and minWidth', () => {
    for (const w of dashboardWidgetRegistry) {
      expect(w.defaultWidth, `${w.type} defaultWidth`).toBeGreaterThanOrEqual(1);
      expect(w.defaultWidth, `${w.type} defaultWidth`).toBeLessThanOrEqual(12);
      expect(w.minWidth, `${w.type} minWidth`).toBeGreaterThanOrEqual(1);
      expect(w.minWidth, `${w.type} minWidth`).toBeLessThanOrEqual(w.defaultWidth);
    }
  });

  test('getDashboardWidgetDefaultWidth returns 12 for unknown types', () => {
    expect(getDashboardWidgetDefaultWidth('nonexistent')).toBe(12);
  });

  test('getDashboardWidgetMinWidth returns 3 for unknown types', () => {
    expect(getDashboardWidgetMinWidth('nonexistent')).toBe(3);
  });

  test('assigns every widget to a known category', () => {
    const knownCategories = Object.values(dashboardWidgetCategories);
    for (const w of dashboardWidgetRegistry) {
      expect(knownCategories, `${w.type} category`).toContain(w.category);
    }
  });

  test('groups the support overview under the support category', () => {
    expect(
      getDashboardWidgetsByCategory(dashboardWidgetCategories.SUPPORT).map((w) => w.type)
    ).toContain('support-overview');
  });
});

describe('buildDefaultDashboardLayout', () => {
  const layout = buildDefaultDashboardLayout();

  test('marks defaults as a 12-column layout', () => {
    expect(layout.grid_columns).toBe(12);
  });

  test('Work section seeds personal, assigned and created items at 6-column widths', () => {
    const workWidgets = layout.widgets.filter((w) => w.section_id === 'default-work');
    expect(workWidgets.map((w) => w.type)).toEqual([
      'personal-tasks',
      'assigned-to-me',
      'created-by-me',
    ]);
    expect(workWidgets.every((w) => w.width === 6)).toBe(true);
  });

  test('Work section subtitle describes both personal and assigned items', () => {
    const workSection = layout.sections.find((s) => s.id === 'default-work');
    expect(workSection.subtitle).not.toBe('Items assigned to you');
    expect(workSection.subtitle.toLowerCase()).toContain('personal');
  });

  test('every seeded widget width fits in the 12-column grid', () => {
    for (const w of layout.widgets) {
      expect(w.width).toBeGreaterThanOrEqual(1);
      expect(w.width).toBeLessThanOrEqual(12);
    }
  });

  test('every widget has an empty config object', () => {
    for (const w of layout.widgets) {
      expect(w.config).toEqual({});
    }
  });

  test('offers saved search without seeding it into the default layout', () => {
    expect(dashboardWidgetRegistry.some((widget) => widget.type === 'saved-search')).toBe(true);
    expect(layout.widgets.some((widget) => widget.type === 'saved-search')).toBe(false);
  });
});
