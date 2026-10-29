import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  getById: vi.fn(),
  getSubmissions: vi.fn(),
  getChannels: vi.fn(),
  update: vi.fn(),
  delete: vi.fn(),
  confirm: vi.fn(),
  contactRolesGetAll: vi.fn(),
}));

vi.mock('../api/portal.js', () => ({
  portalCustomers: mocks,
  contactRoles: { getAll: mocks.contactRolesGetAll },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) =>
    ({
      'common.overview': 'Overview',
      'common.created': 'Created',
      'workspaces.customers.submissions': 'Submissions',
      'workspaces.customers.channels': 'Channels',
      'channels.form': 'Form',
    })[key] ?? key,
}));

vi.mock('../composables/useConfirm.js', () => ({
  confirm: mocks.confirm,
}));

vi.mock('../utils/dateFormatter.js', () => ({
  formatCustomFieldDate: (value) => value === '2026-05-14' ? 'May 14, 2026' : value,
}));

import ContactDetail from './ContactDetail.svelte';
import { contactRoles, portalCustomers } from '../api/portal.js';

describe('ContactDetail activity', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.contactRolesGetAll.mockResolvedValue([]);
    mocks.getById.mockResolvedValue({
      id: 7,
      name: 'Leandro Rivas',
      email: 'leandro@example.com',
    });
    mocks.getSubmissions.mockResolvedValue([
      {
        id: 42,
        workspace_id: 5,
        workspace_item_number: 19,
        workspace_name: 'Support',
        workspace_key: 'SUP',
        title: 'Visible request',
        status_name: 'Open',
        status_color: '#0f766e',
      },
    ]);
    mocks.getChannels.mockResolvedValue([
      {
        id: 99,
        channel_id: 349,
        channel_name: 'Customer requests',
        channel_type: 'form',
      },
    ]);
    mocks.delete.mockResolvedValue(undefined);
    mocks.confirm.mockResolvedValue(true);
  });

  it('opens editing from the customer action menu', async () => {
    render(ContactDetail, { props: { contactId: 7, canManage: true } });

    await fireEvent.click(await screen.findByTestId('customer-detail-actions'));
    await fireEvent.click(await screen.findByTestId('customer-detail-edit'));

    expect(await screen.findByLabelText(/workspaces\.customers\.fields\.name/)).toHaveValue('Leandro Rivas');
  });

  it('renders custom field editors when the customer has no stored value', async () => {
    const fields = [
      { id: 11, name: 'account_tier', label: 'Account tier', field_type: 'text' },
    ];

    render(ContactDetail, { props: { contactId: 7, canManage: true, portalCustomerFields: fields } });
    await fireEvent.click(await screen.findByTestId('customer-detail-actions'));
    await fireEvent.click(await screen.findByTestId('customer-detail-edit'));

    expect(await screen.findByTestId('custom-field-input-11')).toHaveValue('');
  });

  it('assigns contact levels and saves role_ids', async () => {
    mocks.contactRolesGetAll.mockResolvedValue([
      { id: 3, name: 'Manager' },
      { id: 4, name: 'Viewer' },
    ]);
    mocks.getById.mockResolvedValue({
      id: 7,
      name: 'Leandro Rivas',
      email: 'leandro@example.com',
      roles: [{ id: 3, name: 'Manager' }],
    });
    mocks.update.mockResolvedValue({});

    render(ContactDetail, { props: { contactId: 7, canManage: true } });
    await fireEvent.click(await screen.findByTestId('customer-detail-actions'));
    await fireEvent.click(await screen.findByTestId('customer-detail-edit'));

    // Manager is pre-checked from the loaded roles; add Viewer.
    await fireEvent.click(await screen.findByTestId('contact-role-4'));
    await fireEvent.click(screen.getByText('common.saveChanges'));

    await waitFor(() => {
      expect(mocks.update).toHaveBeenCalledWith(7, expect.objectContaining({ role_ids: [3, 4] }));
    });
  });

  it('confirms deletion and returns to the customer list after success', async () => {
    const onBack = vi.fn();
    render(ContactDetail, { props: { contactId: 7, canManage: true, onBack } });

    await fireEvent.click(await screen.findByTestId('customer-detail-actions'));
    await fireEvent.click(await screen.findByTestId('customer-detail-delete'));

    await waitFor(() => {
      expect(mocks.confirm).toHaveBeenCalledWith(expect.objectContaining({
        title: 'workspaces.customers.deleteCustomer',
        variant: 'danger',
      }));
      expect(mocks.delete).toHaveBeenCalledWith(7);
      expect(onBack).toHaveBeenCalledOnce();
    });
  });

  it('renders the submissions the API returns and links each to its item', async () => {
    render(ContactDetail, { props: { contactId: 7 } });
    await screen.findByTestId('customer-detail-overview-tab');
    await fireEvent.click(screen.getByTestId('customer-detail-submissions-tab'));

    const visible = await screen.findByTestId('customer-submission-42');
    expect(visible).toHaveAttribute('href', '/workspaces/5/items/42');
    expect(within(visible).getByText('Visible request')).toBeInTheDocument();
    expect(within(visible).getByText('SUP-19')).toBeInTheDocument();
    expect(within(visible).getByText('Support')).toBeInTheDocument();

    // The backend omits submissions from workspaces the caller cannot view, so
    // the component must not invent a restricted row of its own.
    expect(screen.queryByTestId('customer-submission-43')).not.toBeInTheDocument();
  });

  it('renders channel identity from the customer-channel payload', async () => {
    render(ContactDetail, { props: { contactId: 7 } });
    await screen.findByTestId('customer-detail-overview-tab');
    await fireEvent.click(screen.getByTestId('customer-detail-channels-tab'));

    await waitFor(() => expect(mocks.getChannels).toHaveBeenCalledWith(7));
    const channel = await screen.findByTestId('customer-channel-349');
    expect(channel).toHaveTextContent('Customer requests');
    expect(channel).toHaveTextContent('Form');
    expect(channel).toHaveTextContent('#349');
    expect(channel).not.toHaveTextContent('Channel #99');
  });

  it('formats stored custom field values and omits empty multiselects', async () => {
    mocks.getById.mockResolvedValue({
      id: 7,
      name: 'Leandro Rivas',
      email: 'leandro@example.com',
      custom_field_values: {
        environment: 2,
        services: [1, 3],
        renewal_date: '2026-05-14',
        confirmed: true,
        owner: { id: 9, name: 'Ada Lovelace' },
        empty_services: [],
      },
    });
    const options = JSON.stringify({
      next_id: 4,
      items: [
        { id: 1, label: 'Support' },
        { id: 2, label: 'Production' },
        { id: 3, label: 'Consulting' },
      ],
    });
    const fields = [
      { id: 1, name: 'environment', label: 'Environment', field_type: 'select', options },
      { id: 2, name: 'services', label: 'Services', field_type: 'multiselect', options },
      { id: 3, name: 'renewal_date', label: 'Renewal date', field_type: 'date' },
      { id: 4, name: 'confirmed', label: 'Confirmed', field_type: 'boolean' },
      { id: 5, name: 'owner', label: 'Owner', field_type: 'user' },
      { id: 6, name: 'empty_services', label: 'Empty services', field_type: 'multiselect', options },
    ];

    render(ContactDetail, { props: { contactId: 7, portalCustomerFields: fields } });

    expect(await screen.findByTestId('customer-custom-field-1-value')).toHaveTextContent('Production');
    expect(screen.getByTestId('customer-custom-field-2-value')).toHaveTextContent('Support, Consulting');
    expect(screen.getByTestId('customer-custom-field-3-value')).toHaveTextContent('May 14, 2026');
    expect(screen.getByTestId('customer-custom-field-4-value')).toHaveTextContent('common.yes');
    expect(screen.getByTestId('customer-custom-field-5-value')).toHaveTextContent('Ada Lovelace');
    expect(screen.queryByTestId('customer-custom-field-6-value')).not.toBeInTheDocument();
  });
});
