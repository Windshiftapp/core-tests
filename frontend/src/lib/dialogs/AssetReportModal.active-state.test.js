import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeAll, beforeEach, describe, expect, test, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  update: vi.fn(),
  getAssetSets: vi.fn(),
  getItemTypes: vi.fn(),
  getWorkspaces: vi.fn(),
}));

vi.mock('../api.js', () => ({
  api: {
    assetReports: { update: mocks.update, create: vi.fn() },
    assetSets: { getAll: mocks.getAssetSets },
    itemTypes: { getAll: mocks.getItemTypes },
    workspaces: { getAll: mocks.getWorkspaces },
  },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

import AssetReportModal from './AssetReportModal.svelte';

beforeAll(() => {
  if (!Element.prototype.animate) {
    Element.prototype.animate = () => ({
      finished: Promise.resolve(),
      cancel: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      play: () => {},
      pause: () => {},
    });
  }
});

beforeEach(() => {
  mocks.update.mockReset().mockResolvedValue({});
  mocks.getAssetSets.mockReset().mockResolvedValue([{ id: 2, name: 'Inventory' }]);
  mocks.getItemTypes.mockReset().mockResolvedValue([]);
  mocks.getWorkspaces.mockReset().mockResolvedValue([]);
});

afterEach(() => {
  document.body.innerHTML = '';
});

describe('AssetReportModal active state', () => {
  test('preserves an inactive report when editing', async () => {
    render(AssetReportModal, {
      props: {
        isOpen: true,
        mode: 'edit',
        channelId: 1,
        assetReport: {
          id: 5,
          name: 'Inventory lookup',
          description: '',
          icon: 'Table2',
          color: '#6b7280',
          asset_set_id: 2,
          cql_query: 'name != null',
          run_mode: 'direct',
          is_active: false,
          column_config: ['title'],
          display_order: 1,
        },
        onclose: vi.fn(),
      },
    });

    await fireEvent.click(screen.getByTestId('dialog-confirm'));

    await waitFor(() => expect(mocks.update).toHaveBeenCalledTimes(1));
    expect(mocks.update).toHaveBeenCalledWith(1, 5, expect.objectContaining({ is_active: false }));
  });
});
