import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeAll, beforeEach, describe, expect, test, vi } from 'vitest';

beforeAll(() => {
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {};
  }
  if (!globalThis.ResizeObserver) {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  }
});

const mocks = vi.hoisted(() => ({
  update: vi.fn(),
  create: vi.fn(),
  getAssetSets: vi.fn(),
  getItemTypes: vi.fn(),
  getWorkspaces: vi.fn(),
  getRequestTypes: vi.fn(),
  getRequestTypeFields: vi.fn(),
}));

vi.mock('../api/channels.js', () => ({
  assetReports: { update: mocks.update, create: mocks.create },
  requestTypes: {
        getForChannel: mocks.getRequestTypes,
        getFields: mocks.getRequestTypeFields,
      },
}))

vi.mock('../api/assets.js', () => ({
  assetSets: { getAll: mocks.getAssetSets },
}))

vi.mock('../api/configuration.js', () => ({
  itemTypes: { getAll: mocks.getItemTypes },
}))

vi.mock('../api/workspaces.js', () => ({
  workspaces: { getAll: mocks.getWorkspaces },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

import AssetReportModal from './AssetReportModal.svelte';
import { assetReports, requestTypes } from '../api/channels.js';
import { assetSets } from '../api/assets.js';
import { itemTypes } from '../api/configuration.js';
import { workspaces } from '../api/workspaces.js';




beforeEach(() => {
  mocks.update.mockReset().mockResolvedValue({});
  mocks.create.mockReset().mockResolvedValue({});
  mocks.getAssetSets.mockReset().mockResolvedValue([{ id: 2, name: 'Inventory' }]);
  mocks.getItemTypes.mockReset().mockResolvedValue([{ id: 3, name: 'Request' }]);
  mocks.getWorkspaces.mockReset().mockResolvedValue([{ id: 4, name: 'Support' }]);
  mocks.getRequestTypes.mockReset().mockResolvedValue([]);
  mocks.getRequestTypeFields.mockReset().mockResolvedValue([]);
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

describe('AssetReportModal row actions', () => {
  function renderWithRowAction(config) {
    return render(AssetReportModal, {
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
          cql_query: 'status = "Active"',
          run_mode: 'direct',
          column_config: ['title'],
          display_order: 1,
          config,
        },
        onclose: vi.fn(),
      },
    });
  }

  test('round-trips an edited row action into the submitted config', async () => {
    mocks.getRequestTypes.mockResolvedValue([{ id: 12, name: 'Hardware problem' }]);
    mocks.getRequestTypeFields.mockResolvedValue([
      { id: 1, field_identifier: 'device', field_label: 'Device', field_type: 'custom' },
    ]);
    const config = JSON.stringify({
      row_actions: [
        {
          id: 'ra1',
          label: 'Old label',
          request_type_id: 12,
          target_field: 'device',
          source: 'asset_id',
        },
      ],
    });
    renderWithRowAction(config);

    await fireEvent.input(await screen.findByTestId('asset-report-row-action-label-0'), {
      target: { value: ' Report an issue ' },
    });
    await fireEvent.click(screen.getByTestId('dialog-confirm'));

    await waitFor(() => expect(mocks.update).toHaveBeenCalledTimes(1));
    const submitted = JSON.parse(mocks.update.mock.calls[0][2].config);
    expect(submitted.row_actions).toEqual([
      {
        id: 'ra1',
        label: 'Report an issue',
        request_type_id: 12,
        target_field: 'device',
        source: 'asset_id',
      },
    ]);
  });

  test('removes a row action before submit', async () => {
    const config = JSON.stringify({
      row_actions: [
        {
          id: 'ra1',
          label: 'Report an issue',
          request_type_id: 12,
          target_field: 'device',
          source: 'asset_id',
        },
      ],
    });
    renderWithRowAction(config);

    await fireEvent.click(await screen.findByTestId('asset-report-row-action-remove-0'));
    await fireEvent.click(screen.getByTestId('dialog-confirm'));

    await waitFor(() => expect(mocks.update).toHaveBeenCalledTimes(1));
    const submittedConfig = mocks.update.mock.calls[0][2].config;
    const submitted = submittedConfig ? JSON.parse(submittedConfig) : null;
    expect(submitted === null || submitted.row_actions === undefined).toBe(true);
  });

  test('loads request-type fields when a request type is picked for a row action', async () => {
    mocks.getRequestTypes.mockResolvedValue([{ id: 12, name: 'Hardware problem' }]);
    mocks.getRequestTypeFields.mockResolvedValue([
      { id: 1, field_identifier: 'device', field_label: 'Device', field_type: 'custom' },
    ]);
    renderWithRowAction(null);

    await fireEvent.click(screen.getByTestId('asset-report-add-row-action'));
    const requestTypeInput = await screen.findByTestId(
      'asset-report-row-action-request-type-0'
    );
    await fireEvent.click(requestTypeInput);
    await fireEvent.input(requestTypeInput, { target: { value: 'Hardware' } });

    await waitFor(() =>
      expect(document.querySelector('[data-option-value="12"]')).toBeInTheDocument()
    );
    await fireEvent.click(document.querySelector('[data-option-value="12"]'));

    await waitFor(() => expect(mocks.getRequestTypeFields).toHaveBeenCalledWith(12));
    await fireEvent.click(screen.getByTestId('asset-report-row-action-target-field-0'));
    await waitFor(() =>
      expect(document.querySelector('[data-option-value="device"]')).toBeInTheDocument()
    );
  });

  test('rejects a partially filled row action instead of dropping it on save', async () => {
    renderWithRowAction(null);

    await fireEvent.click(screen.getByTestId('asset-report-add-row-action'));
    await fireEvent.input(await screen.findByTestId('asset-report-row-action-label-0'), {
      target: { value: 'Report an issue' },
    });
    await fireEvent.click(screen.getByTestId('dialog-confirm'));

    expect(mocks.update).not.toHaveBeenCalled();
    expect(screen.getByText('portal.rowActionIncomplete')).toBeInTheDocument();
  });

  test('adds an empty action row that does not reach the config until filled', async () => {
    renderWithRowAction(null);

    await fireEvent.click(screen.getByTestId('asset-report-add-row-action'));
    expect(await screen.findByTestId('asset-report-row-action-0')).toBeInTheDocument();

    await fireEvent.click(screen.getByTestId('dialog-confirm'));
    await waitFor(() => expect(mocks.update).toHaveBeenCalledTimes(1));
    const submitted = mocks.update.mock.calls[0][2].config;
    expect(submitted === null || JSON.parse(submitted).row_actions === undefined).toBe(true);
  });
});
