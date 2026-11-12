import { fireEvent, render } from '@testing-library/svelte';
import { readable } from 'svelte/store';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  intakesList: vi.fn(),
  getItemTypes: vi.fn(),
  errorToast: vi.fn(),
}));

vi.mock('../../stores/i18n.svelte.js', () => ({ t: (key) => key }));
vi.mock('../../stores/permissions.svelte.js', () => ({ isSystemAdmin: readable(true) }));
vi.mock('../../stores/toasts.svelte.js', () => ({
  surfaceError: vi.fn(),
  errorToast: mocks.errorToast,
  successToast: vi.fn(),
}));
vi.mock('../../api/channels.js', () => ({
  channelIntakes: { list: mocks.intakesList },
}))

vi.mock('../../api/workspaces.js', () => ({
  workspaces: { getItemTypes: mocks.getItemTypes },
}));

import ChannelEmailConfig from './ChannelEmailConfig.svelte';
import { channelIntakes } from '../../api/channels.js';
import { workspaces } from '../../api/workspaces.js';


function basicFormData(overrides = {}) {
  return {
    auth_method: 'basic',
    imap_host: 'imap.example.com',
    imap_username: 'support@example.com',
    imap_password: 'secret',
    imap_port: 993,
    imap_encryption: 'ssl',
    workspace_id: null,
    item_type_id: null,
    mailbox: 'INBOX',
    processing_disposition: 'mark_read',
    rate_limit_per_hour: null,
    enabled: false,
    ...overrides,
  };
}

function oauthFormData(overrides = {}) {
  return {
    auth_method: 'oauth',
    oauth_provider_type: 'microsoft',
    oauth_client_id: 'client-id',
    oauth_client_secret: '',
    oauth_client_secret_stored: false,
    stored_oauth_provider_type: '',
    stored_oauth_client_id: '',
    oauth_tenant_id: 'common',
    oauth_connected: false,
    oauth_email: '',
    oauth_mailbox: 'support@example.com',
    connected_oauth_provider_type: '',
    connected_oauth_client_id: '',
    connected_oauth_tenant_id: '',
    connected_oauth_mailbox: '',
    processing_disposition: 'mark_read',
    enabled: false,
    ...overrides,
  };
}

describe('ChannelEmailConfig mailbox disposition', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.intakesList.mockResolvedValue([]);
    mocks.getItemTypes.mockResolvedValue([]);
  });

  it('persists the mailbox-level processing disposition in the saved config', () => {
    const { component } = render(ChannelEmailConfig, {
      props: { channelId: 7, formData: basicFormData() },
    });

    expect(component.getConfig().email_processing_disposition).toBe('mark_read');
  });

  it('writes a changed disposition into the saved config', async () => {
    const { component } = render(ChannelEmailConfig, {
      props: { channelId: 7, formData: basicFormData() },
    });

    await fireEvent.click(document.querySelector('#email-processing-disposition'));
    await fireEvent.click(document.querySelector('[data-option-id="leave"]'));

    expect(component.getConfig().email_processing_disposition).toBe('leave');
  });
});

describe('ChannelEmailConfig OAuth client secret retention', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.intakesList.mockResolvedValue([]);
    mocks.getItemTypes.mockResolvedValue([]);
  });

  it('accepts a stored secret without requiring re-entry', () => {
    const { component } = render(ChannelEmailConfig, {
      props: {
        channelId: 7,
        formData: oauthFormData({
          oauth_client_secret_stored: true,
          stored_oauth_provider_type: 'microsoft',
          stored_oauth_client_id: 'client-id',
        }),
      },
    });

    expect(component.validate()).toEqual({ valid: true });
  });

  it('requires a client secret when none is stored', () => {
    const { component } = render(ChannelEmailConfig, {
      props: { channelId: 7, formData: oauthFormData() },
    });

    expect(component.validate()).toEqual({ valid: false, message: 'channel.clientSecretRequired' });
  });

  it('requires a client secret again when the client identity changed', () => {
    const { component } = render(ChannelEmailConfig, {
      props: {
        channelId: 7,
        formData: oauthFormData({
          oauth_client_secret_stored: true,
          stored_oauth_provider_type: 'microsoft',
          stored_oauth_client_id: 'previous-client-id',
        }),
      },
    });

    expect(component.validate()).toEqual({ valid: false, message: 'channel.clientSecretRequired' });
  });

  it('records a just-saved secret and clears the field', () => {
    const formData = oauthFormData({
      oauth_client_secret: 'typed-secret',
      stored_oauth_provider_type: 'microsoft',
      stored_oauth_client_id: 'client-id',
    });
    const { component } = render(ChannelEmailConfig, {
      props: { channelId: 7, formData },
    });

    component.clearSecrets();

    expect(formData.oauth_client_secret).toBe('');
    expect(formData.oauth_client_secret_stored).toBe(true);
  });
});
