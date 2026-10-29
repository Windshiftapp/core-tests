import { fireEvent, render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => {
  const makeStore = (value) => ({
    subscribe: (run) => {
      run(value);
      return () => {};
    },
  });
  return {
    currentRoute: makeStore({ view: null, params: {}, query: {} }),
    permissionStore: makeStore({ userPermissionKeys: new Set(['customers.manage']) }),
    isSystemAdmin: makeStore(false),
    navigate: vi.fn(),
    getOrganisations: vi.fn(),
    getCustomers: vi.fn(),
    getCustomFields: vi.fn(),
    errorToast: vi.fn(),
  };
});

vi.mock('../router.js', () => ({
  currentRoute: mocks.currentRoute,
  navigate: mocks.navigate,
}));

vi.mock('../api/portal.js', () => ({
  customerOrganisations: { getAll: mocks.getOrganisations },
  portalCustomers: {
        getAll: mocks.getCustomers,
        create: vi.fn(),
        delete: vi.fn(),
        updateOrganisation: vi.fn(),
        deactivate: vi.fn(),
        activate: vi.fn(),
      },
}))

vi.mock('../api/configuration.js', () => ({
  customFields: { getAll: mocks.getCustomFields },
}));

vi.mock('../stores/permissions.svelte.js', () => ({
  permissionStore: mocks.permissionStore,
  isSystemAdmin: mocks.isSystemAdmin,
}));

vi.mock('../stores/toasts.svelte.js', () => ({
  errorToast: mocks.errorToast,
  successToast: vi.fn(),
  warningToast: vi.fn(),
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('../composables/useConfirm.js', () => ({
  confirm: vi.fn().mockResolvedValue(true),
}));

vi.mock('@atlaskit/pragmatic-drag-and-drop/element/adapter', () => ({
  draggable: vi.fn(() => () => {}),
  dropTargetForElements: vi.fn(() => () => {}),
}));

import Customers from './Customers.svelte';
import { customerOrganisations, portalCustomers } from '../api/portal.js';
import { customFields } from '../api/configuration.js';


describe('Customers create modal', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.getOrganisations.mockResolvedValue([]);
    mocks.getCustomers.mockResolvedValue([]);
    mocks.getCustomFields.mockResolvedValue([]);
  });

  it('renders a custom field editor when a new customer has no stored value', async () => {
    mocks.getCustomFields.mockResolvedValue([
      {
        id: 41,
        name: 'account_tier',
        label: 'Account tier',
        field_type: 'text',
        applies_to_portal_customers: true,
      },
    ]);

    render(Customers);
    await screen.findByTestId('portal-customers-page');
    await fireEvent.click(await screen.findByText('workspaces.customers.addCustomer'));

    expect(await screen.findByTestId('custom-field-input-41')).toHaveValue('');
  });
});
