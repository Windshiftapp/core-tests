import { render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  execute: vi.fn(),
  getCustomFields: vi.fn(),
}));

vi.mock('../api/channels.js', () => ({
  assetReports: {
        execute: mocks.execute,
        submit: vi.fn(),
        getPortalFields: vi.fn(),
      },
}))

vi.mock('../api/portal.js', () => ({
  portal: {
        getCustomFields: mocks.getCustomFields,
      },
}));

vi.mock('../stores/portal.svelte.js', () => ({
  portalStore: { isDarkMode: false },
  portalCustomizationStore: { isDarkMode: false },
  iconMap: {},
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('../utils/dateFormatter.js', () => ({
  formatCustomFieldDate: (value) => value === '2026-05-14' ? 'May 14, 2026' : value,
}));

import AssetReportTable from './AssetReportTable.svelte';
import { assetReports } from '../api/channels.js';
import { portal } from '../api/portal.js';


describe('AssetReportTable custom fields', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.getCustomFields.mockResolvedValue([
      { id: 7, name: 'Environment', field_type: 'select', options: JSON.stringify({ next_id: 3, items: [{ id: 1, label: 'Staging' }, { id: 2, label: 'Production' }] }) },
      { id: 8, name: 'Services', field_type: 'multiselect', options: JSON.stringify({ next_id: 4, items: [{ id: 1, label: 'Support' }, { id: 3, label: 'Consulting' }] }) },
      { id: 9, name: 'Confirmed', field_type: 'boolean' },
      { id: 10, name: 'Renewal date', field_type: 'date' },
    ]);
    mocks.execute.mockResolvedValue({
      assets: [{
        id: 42,
        custom_field_values: {
          7: 2,
          8: [1, 3],
          9: true,
          10: '2026-05-14',
        },
      }],
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
    expect(screen.getByTestId('asset-report-cell-42-cf_8')).toHaveTextContent('Support, Consulting');
    expect(screen.getByTestId('asset-report-cell-42-cf_9')).toHaveTextContent('common.yes');
    expect(screen.getByTestId('asset-report-cell-42-cf_10')).toHaveTextContent('May 14, 2026');
    expect(mocks.getCustomFields).toHaveBeenCalledWith('support');
  });

  it('does not badge a public report whose payload omits is_active', async () => {
    render(AssetReportTable, {
      props: {
        slug: 'support',
        sectionId: 'assets',
        report: { id: 6, name: 'Public assets', column_config: ['title'] },
      },
    });

    expect(await screen.findByText('Public assets')).toBeInTheDocument();
    expect(screen.queryByText('INACTIVE')).not.toBeInTheDocument();
  });

  it('badges a report explicitly marked inactive', async () => {
    render(AssetReportTable, {
      props: {
        slug: 'support',
        sectionId: 'assets',
        report: { id: 7, name: 'Retired assets', is_active: false, column_config: ['title'] },
      },
    });

    expect(await screen.findByText('Retired assets')).toBeInTheDocument();
    expect(screen.getByText('INACTIVE')).toBeInTheDocument();
  });
});

describe('AssetReportTable row actions', () => {
  beforeEach(() => {
    mocks.execute.mockResolvedValue({
      assets: [{ id: 42, title: 'Laptop', asset_tag: 'LT-0042' }],
      total: 1,
      total_pages: 1,
    });
  });

  it('renders an action column linking to the request route with the asset id prefill', async () => {
    render(AssetReportTable, {
      props: {
        slug: 'support',
        sectionId: 'assets',
        report: {
          id: 8,
          name: 'Devices',
          column_config: ['title'],
          config: {
            row_actions: [
              {
                id: 'ra1',
                label: 'Report an issue',
                request_type_id: 12,
                target_field: 'device',
                source: 'asset_id',
              },
            ],
          },
        },
      },
    });

    expect(await screen.findByTestId('asset-report-action-column-ra1')).toHaveTextContent(
      'Report an issue'
    );
    expect(screen.getByTestId('asset-report-action-42-ra1')).toHaveAttribute(
      'href',
      '/portal/support/request/12?prefill.device=42'
    );
  });

  it('uses the configured asset tag source for the prefill value', async () => {
    render(AssetReportTable, {
      props: {
        slug: 'support',
        sectionId: 'assets',
        report: {
          id: 9,
          name: 'Devices',
          column_config: ['title'],
          config: {
            row_actions: [
              {
                id: 'ra2',
                label: 'Open request',
                request_type_id: 13,
                target_field: 'asset_tag',
                source: 'asset_tag',
              },
            ],
          },
        },
      },
    });

    expect(await screen.findByTestId('asset-report-action-42-ra2')).toHaveAttribute(
      'href',
      '/portal/support/request/13?prefill.asset_tag=LT-0042'
    );
  });

  it('renders no action column when the report has no row actions', async () => {
    render(AssetReportTable, {
      props: {
        slug: 'support',
        sectionId: 'assets',
        report: { id: 10, name: 'Devices', column_config: ['title'] },
      },
    });

    expect(await screen.findByText('Devices')).toBeInTheDocument();
    expect(screen.queryByTestId(/asset-report-action-column/)).toBeNull();
  });
});
