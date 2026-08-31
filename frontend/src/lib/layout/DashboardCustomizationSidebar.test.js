import { render, within } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';

vi.mock('../stores/i18n.svelte.js', async (importOriginal) => ({
  ...(await importOriginal()),
  t: (key, params = {}) =>
    key === 'widgets.defaultWidth' ? `Default: ${params.width}/${params.columns} width` : key,
}));

import DashboardCustomizationSidebar from './DashboardCustomizationSidebar.svelte';

describe('DashboardCustomizationSidebar', () => {
  it('describes widget widths on the dashboard grid scale', () => {
    const { container } = render(DashboardCustomizationSidebar, {
      isOpen: true,
      activeCategory: 'activity',
    });

    const dailyBriefing = container.querySelector('[data-widget-type="daily-briefing"]');
    expect(dailyBriefing).not.toBeNull();
    expect(within(dailyBriefing).getByText('Default: 12/12 width')).toBeInTheDocument();
  });
});
