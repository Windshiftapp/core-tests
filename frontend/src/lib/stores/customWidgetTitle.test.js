import { beforeEach, describe, expect, it, vi } from 'vitest';

// The store persists through the dashboard layout API; the test only cares
// about the in-memory update.
vi.mock('../api/misc.js', () => ({
  userPreferences: { updateDashboardLayout: vi.fn().mockResolvedValue({}) },
}));

import { homepageStore } from './homepageStore.svelte.js';

describe('homepageStore.setCustomWidgetTitle', () => {
  beforeEach(() => {
    homepageStore.widgets = [
      { id: 'w1', type: 'custom-widget', section_id: 's1', position: 0, width: 6, config: { customWidgetId: 4, title: 'Old name' } },
      { id: 'w2', type: 'custom-widget', section_id: 's1', position: 1, width: 6, config: { customWidgetId: 9, title: 'Other widget' } },
      { id: 'w3', type: 'daily-briefing', section_id: 's1', position: 2, width: 12, config: {} },
    ];
    homepageStore.sections = [{ id: 's1', title: 'S', subtitle: '', display_order: 0, widget_ids: ['w1', 'w2', 'w3'] }];
  });

  it('renames every card bound to that widget and leaves the others alone', () => {
    homepageStore.setCustomWidgetTitle(4, 'New name');

    const byId = Object.fromEntries(homepageStore.widgets.map((w) => [w.id, w]));
    expect(byId.w1.config.title).toBe('New name');
    expect(byId.w2.config.title).toBe('Other widget');
    expect(byId.w3.config).toEqual({});
  });

  it('ignores a missing title or widget id', () => {
    homepageStore.setCustomWidgetTitle(4, '');
    homepageStore.setCustomWidgetTitle(null, 'x');
    expect(homepageStore.widgets[0].config.title).toBe('Old name');
  });
});
