import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  getTickets: vi.fn(),
  setRequestSharing: vi.fn(),
  contactRolesGetAll: vi.fn(),
  listDocuments: vi.fn(),
  errorToast: vi.fn(),
  successToast: vi.fn(),
}));

vi.mock('../api/portal.js', () => ({
  customerOrganisations: {
        getTickets: mocks.getTickets,
        setRequestSharing: mocks.setRequestSharing,
      },
  contactRoles: { getAll: mocks.contactRolesGetAll },
}));

vi.mock('../api/logbook.js', () => ({
  logbook: { listDocumentsByOrganisation: mocks.listDocuments },
}));

vi.mock('../stores/capabilities.svelte.js', () => ({
  capabilitiesStore: { logbookAvailable: false },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('../stores/toasts.svelte.js', () => ({
  errorToast: mocks.errorToast,
  successToast: mocks.successToast,
}));

vi.mock('../utils/urls.js', () => ({
  itemUrl: ({ workspaceId, itemId }) => `/w/${workspaceId}/items/${itemId}`,
}));

vi.mock('../utils/dateFormatter.js', () => ({
  formatDateShort: (value) => String(value),
}));

import OrganisationDetail from './OrganisationDetail.svelte';
import { contactRoles, customerOrganisations } from '../api/portal.js';
import { logbook } from '../api/logbook.js';


const orgA = { id: 1, name: 'Org A', settings: { request_sharing: 'disabled' } };
const orgB = { id: 2, name: 'Org B', settings: { request_sharing: 'disabled' } };

beforeEach(() => {
  vi.clearAllMocks();
  mocks.getTickets.mockImplementation((orgId) =>
    Promise.resolve(
      orgId === 1
        ? [{ id: 11, title: 'Ticket A', workspace_id: 5, workspace_item_number: 1, workspace_key: 'WI' }]
        : [{ id: 22, title: 'Ticket B', workspace_id: 5, workspace_item_number: 2, workspace_key: 'WI' }]
    )
  );
  mocks.contactRolesGetAll.mockResolvedValue([]);
  mocks.setRequestSharing.mockResolvedValue({});
});

describe('OrganisationDetail tickets tab', () => {
  it('reloads tickets when the organisation changes', async () => {
    const { rerender } = render(OrganisationDetail, {
      props: { organisation: orgA, customers: [], canManage: true },
    });

    await fireEvent.click(await screen.findByTestId('org-detail-tickets-tab'));
    expect(await screen.findByText('Ticket A')).toBeInTheDocument();
    expect(mocks.getTickets).toHaveBeenCalledWith(1);

    await rerender({ organisation: orgB, customers: [], canManage: true });

    expect(await screen.findByText('Ticket B')).toBeInTheDocument();
    expect(mocks.getTickets).toHaveBeenCalledWith(2);
    expect(screen.queryByText('Ticket A')).not.toBeInTheDocument();
  });

  it('does not let a late response for the previous organisation populate the new one', async () => {
    let resolveA;
    mocks.getTickets.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveA = resolve;
        })
    );

    const { rerender } = render(OrganisationDetail, {
      props: { organisation: orgA, customers: [], canManage: true },
    });
    await fireEvent.click(await screen.findByTestId('org-detail-tickets-tab'));
    await waitFor(() => expect(mocks.getTickets).toHaveBeenCalledWith(1));

    await rerender({ organisation: orgB, customers: [], canManage: true });
    expect(await screen.findByText('Ticket B')).toBeInTheDocument();

    resolveA([{ id: 11, title: 'Ticket A', workspace_id: 5, workspace_item_number: 1, workspace_key: 'WI' }]);
    await waitFor(() => expect(mocks.getTickets).toHaveBeenCalledWith(2));
    expect(screen.queryByText('Ticket A')).not.toBeInTheDocument();
    expect(screen.getByText('Ticket B')).toBeInTheDocument();
  });
});

describe('OrganisationDetail sharing tab', () => {
  it('reports the saved settings back to the parent so a revisit is not stale', async () => {
    const onOrganisationUpdated = vi.fn();
    const savedOrg = {
      id: 1,
      name: 'Org A',
      settings: { request_sharing: 'automatic', request_sharing_audience: 'organisation' },
    };
    render(OrganisationDetail, {
      props: { organisation: savedOrg, customers: [], canManage: true, onOrganisationUpdated },
    });

    await fireEvent.click(await screen.findByTestId('org-detail-sharing-tab'));
    await fireEvent.click(screen.getByTestId('org-sharing-save'));

    await waitFor(() =>
      expect(mocks.setRequestSharing).toHaveBeenCalledWith(1, {
        request_sharing: 'automatic',
        request_sharing_audience: 'organisation',
        visible_role_ids: [],
      })
    );
    expect(onOrganisationUpdated).toHaveBeenCalledWith(
      1,
      expect.objectContaining({ request_sharing: 'automatic', request_sharing_audience: 'organisation' })
    );
  });
});
