import { render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  execute: vi.fn(),
  getCustomFields: vi.fn(),
}));

vi.mock('../api.js', () => ({
  api: {
    assetReports: {
      execute: mocks.execute,
      submit: vi.fn(),
      getPortalFields: vi.fn(),
    },
    portal: {
      getCustomFields: mocks.getCustomFields,
    },
  },
}));

vi.mock('../stores/portal.svelte.js', () => ({
  portalStore: { isDarkMode: false },
  iconMap: {},
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('../utils/dateFormatter.js', () => ({
  formatCustomFieldDate: (value) => (value === '2026-05-14' ? 'May 14, 2026' : value),
}));

import AssetReportTable from './AssetReportTable.svelte';

describe('AssetReportTable custom fields', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.getCustomFields.mockResolvedValue([
      {
        id: 7,
        name: 'Environment',
        field_type: 'select',
        options: JSON.stringify({
          next_id: 3,
          items: [
            { id: 1, label: 'Staging' },
            { id: 2, label: 'Production' },
          ],
        }),
      },
      {
        id: 8,
        name: 'Services',
        field_type: 'multiselect',
        options: JSON.stringify({
          next_id: 4,
          items: [
            { id: 1, label: 'Support' },
            { id: 3, label: 'Consulting' },
          ],
        }),
      },
      { id: 9, name: 'Confirmed', field_type: 'boolean' },
      { id: 10, name: 'Renewal date', field_type: 'date' },
    ]);
    mocks.execute.mockResolvedValue({
      assets: [
        {
          id: 42,
          custom_field_values: {
            7: 2,
            8: [1, 3],
            9: true,
            10: '2026-05-14',
          },
        },
      ],
      total: 1,
      total_pages: 1,
    });
  });

  it('uses field names and formats stored values instead of exposing IDs', async () => {
    render(AssetReportTable, {
      props: {
        slug: 'support',
        sectionId: 'assets',
        report: {
          id: 5,
          name: 'Production assets',
          is_active: true,
          column_config: ['cf_7', 'cf_8', 'cf_9', 'cf_10'],
        },
      },
    });

    expect(await screen.findByTestId('asset-report-column-cf_7')).toHaveTextContent('Environment');
    expect(screen.getByTestId('asset-report-column-cf_8')).toHaveTextContent('Services');
    expect(screen.getByTestId('asset-report-cell-42-cf_7')).toHaveTextContent('Production');
    expect(screen.getByTestId('asset-report-cell-42-cf_8')).toHaveTextContent(
      'Support, Consulting'
    );
    expect(screen.getByTestId('asset-report-cell-42-cf_9')).toHaveTextContent('common.yes');
    expect(screen.getByTestId('asset-report-cell-42-cf_10')).toHaveTextContent('May 14, 2026');
    expect(mocks.getCustomFields).toHaveBeenCalledWith('support');
  });
});
