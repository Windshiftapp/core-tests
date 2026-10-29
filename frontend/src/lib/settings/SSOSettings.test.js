import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { ssoStore } = vi.hoisted(() => ({
  ssoStore: {
    loadProviders: vi.fn(),
    createProvider: vi.fn(),
    updateProvider: vi.fn(),
    deleteProvider: vi.fn(),
    testProvider: vi.fn(),
    subscribe: vi.fn(),
  },
}));

vi.mock('../stores/sso.svelte.js', () => ({
  ssoStore,
}))

vi.mock('../stores/capabilities.svelte.js', () => ({
  capabilitiesStore: {
      has: vi.fn(() => false),
    },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

import SSOSettings from './SSOSettings.svelte';

describe('SSOSettings provider submission errors', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    ssoStore.loadProviders.mockResolvedValue(undefined);
    ssoStore.subscribe.mockImplementation((subscriber) => {
      subscriber({ adminProviders: [] });
      return () => {};
    });
  });

  it('shows a create failure inside the open provider modal', async () => {
    const providerError = 'OIDC discovery failed. Verify the issuer URL.';
    ssoStore.createProvider.mockRejectedValue(new Error(providerError));

    render(SSOSettings);

    const addButtons = await screen.findAllByRole('button', {
      name: 'settings.sso.addProvider',
    });
    await fireEvent.click(addButtons[0]);

    const dialog = screen.getByRole('dialog');
    await fireEvent.input(within(dialog).getByLabelText(/settings\.sso\.displayName/), {
      target: { value: 'Entra' },
    });
    await fireEvent.input(within(dialog).getByLabelText(/settings\.sso\.issuerUrl/), {
      target: { value: 'https://login.example.test/tenant' },
    });
    await fireEvent.input(within(dialog).getByLabelText(/settings\.sso\.clientId/), {
      target: { value: 'client-id' },
    });
    await fireEvent.input(within(dialog).getByLabelText(/settings\.sso\.clientSecret/), {
      target: { value: 'client-secret' },
    });
    await fireEvent.click(
      within(dialog).getByRole('button', { name: 'settings.sso.createProvider' })
    );

    await waitFor(() => {
      expect(ssoStore.createProvider).toHaveBeenCalledOnce();
      expect(within(dialog).getByText(providerError)).toBeInTheDocument();
    });
    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });

  it('shows an update failure inside the open provider modal', async () => {
    const provider = {
      id: 7,
      name: 'Entra',
      slug: 'entra',
      provider_type: 'oidc',
      enabled: true,
      is_default: true,
      issuer_url: 'https://login.example.test/tenant',
      client_id: 'client-id',
      auto_provision_users: false,
      require_verified_email: true,
    };
    const providerError = 'Failed to connect to OIDC provider.';
    ssoStore.subscribe.mockImplementation((subscriber) => {
      subscriber({ adminProviders: [provider] });
      return () => {};
    });
    ssoStore.updateProvider.mockRejectedValue(new Error(providerError));

    render(SSOSettings);

    await fireEvent.click(await screen.findByRole('button', { name: 'common.edit' }));
    const dialog = screen.getByRole('dialog');
    await fireEvent.click(within(dialog).getByRole('button', { name: 'common.saveChanges' }));

    await waitFor(() => {
      expect(ssoStore.updateProvider).toHaveBeenCalledOnce();
      expect(within(dialog).getByText(providerError)).toBeInTheDocument();
    });
    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });
});

describe('SSOSettings attribute mapping', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    ssoStore.loadProviders.mockResolvedValue(undefined);
    ssoStore.subscribe.mockImplementation((subscriber) => {
      subscriber({ adminProviders: [] });
      return () => {};
    });
  });

  it('loads an existing mapping into the fields and serializes edits on save', async () => {
    const provider = {
      id: 8,
      name: 'Entra',
      slug: 'entra',
      provider_type: 'oidc',
      enabled: true,
      is_default: true,
      issuer_url: 'https://login.example.test/tenant',
      client_id: 'client-id',
      auto_provision_users: false,
      require_verified_email: true,
      attribute_mapping: JSON.stringify({ given_name: 'first_name', family_name: 'last_name' }),
    };
    ssoStore.subscribe.mockImplementation((subscriber) => {
      subscriber({ adminProviders: [provider] });
      return () => {};
    });
    ssoStore.updateProvider.mockResolvedValue(undefined);

    render(SSOSettings);

    await fireEvent.click(await screen.findByRole('button', { name: 'common.edit' }));
    const dialog = screen.getByRole('dialog');

    expect(within(dialog).getByLabelText('settings.sso.attributeGivenName')).toHaveValue('first_name');
    expect(within(dialog).getByLabelText('settings.sso.attributeFamilyName')).toHaveValue('last_name');
    expect(within(dialog).getByLabelText('settings.sso.attributeEmail')).toHaveValue('');

    await fireEvent.input(within(dialog).getByLabelText('settings.sso.attributeEmail'), {
      target: { value: 'mail' },
    });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'common.saveChanges' }));

    await waitFor(() => expect(ssoStore.updateProvider).toHaveBeenCalledOnce());
    const [id, payload] = ssoStore.updateProvider.mock.calls[0];
    expect(id).toBe(8);
    expect(JSON.parse(payload.attribute_mapping)).toEqual({
      email: 'mail',
      given_name: 'first_name',
      family_name: 'last_name',
    });
  });

  it('trims and omits blank mapping fields when creating a provider', async () => {
    ssoStore.createProvider.mockResolvedValue(undefined);

    render(SSOSettings);

    const addButtons = await screen.findAllByRole('button', {
      name: 'settings.sso.addProvider',
    });
    await fireEvent.click(addButtons[0]);
    const dialog = screen.getByRole('dialog');

    await fireEvent.input(within(dialog).getByLabelText(/settings\.sso\.displayName/), {
      target: { value: 'Entra' },
    });
    await fireEvent.input(within(dialog).getByLabelText(/settings\.sso\.issuerUrl/), {
      target: { value: 'https://login.example.test/tenant' },
    });
    await fireEvent.input(within(dialog).getByLabelText(/settings\.sso\.clientId/), {
      target: { value: 'client-id' },
    });
    await fireEvent.input(within(dialog).getByLabelText(/settings\.sso\.clientSecret/), {
      target: { value: 'client-secret' },
    });
    await fireEvent.input(within(dialog).getByLabelText('settings.sso.attributeGivenName'), {
      target: { value: '  first_name  ' },
    });
    await fireEvent.click(
      within(dialog).getByRole('button', { name: 'settings.sso.createProvider' })
    );

    await waitFor(() => expect(ssoStore.createProvider).toHaveBeenCalledOnce());
    const payload = ssoStore.createProvider.mock.calls[0][0];
    expect(JSON.parse(payload.attribute_mapping)).toEqual({ given_name: 'first_name' });
  });

  it('sends an empty mapping object so clearing the fields persists', async () => {
    const provider = {
      id: 9,
      name: 'Entra',
      slug: 'entra',
      provider_type: 'oidc',
      enabled: true,
      is_default: true,
      issuer_url: 'https://login.example.test/tenant',
      client_id: 'client-id',
      auto_provision_users: false,
      require_verified_email: true,
      attribute_mapping: JSON.stringify({ given_name: 'first_name' }),
    };
    ssoStore.subscribe.mockImplementation((subscriber) => {
      subscriber({ adminProviders: [provider] });
      return () => {};
    });
    ssoStore.updateProvider.mockResolvedValue(undefined);

    render(SSOSettings);

    await fireEvent.click(await screen.findByRole('button', { name: 'common.edit' }));
    const dialog = screen.getByRole('dialog');
    await fireEvent.input(within(dialog).getByLabelText('settings.sso.attributeGivenName'), {
      target: { value: '' },
    });
    await fireEvent.click(within(dialog).getByRole('button', { name: 'common.saveChanges' }));

    await waitFor(() => expect(ssoStore.updateProvider).toHaveBeenCalledOnce());
    const [, payload] = ssoStore.updateProvider.mock.calls[0];
    expect(payload.attribute_mapping).toBe('{}');
  });
});
