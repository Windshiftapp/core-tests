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
  mocks.getItemTypes.mockReset().mockResolvedValue([{ id: 3, name: 'Request' }]);
  mocks.getWorkspaces.mockReset().mockResolvedValue([{ id: 4, name: 'Support' }]);
});

afterEach(() => {
  document.body.innerHTML = '';
});

function renderModal(cqlQuery) {
  return render(AssetReportModal, {
    props: {
      isOpen: true,
      mode: 'edit',
      channelId: 1,
      channelWorkspaceIds: [4],
      assetReport: {
        id: 5,
        name: 'Inventory lookup',
        description: '',
        icon: 'Table2',
        color: '#6b7280',
        asset_set_id: 2,
        cql_query: cqlQuery,
        run_mode: 'form',
        item_type_id: 3,
        workspace_id: 4,
        column_config: ['title'],
        display_order: 1,
      },
      onclose: vi.fn(),
    },
  });
}

describe('AssetReportModal form query placeholders', () => {
  test('rejects a bare-brace placeholder that the backend cannot substitute', async () => {
    renderModal('status = {status}');

    await fireEvent.click(screen.getByTestId('dialog-confirm'));

    expect(mocks.update).not.toHaveBeenCalled();
    expect(screen.getByText('portal.qlQueryTokenRequired')).toBeInTheDocument();
  });

  test('accepts the backend placeholder syntax including dashed identifiers', async () => {
    renderModal(`asset_tag = \${asset-tag}`);

    await fireEvent.click(screen.getByTestId('dialog-confirm'));

    await waitFor(() => expect(mocks.update).toHaveBeenCalledTimes(1));
    expect(mocks.update).toHaveBeenCalledWith(
      1,
      5,
      expect.objectContaining({ cql_query: `asset_tag = \${asset-tag}` })
    );
    expect(screen.queryByText('portal.qlQueryTokenRequired')).not.toBeInTheDocument();
  });
});
