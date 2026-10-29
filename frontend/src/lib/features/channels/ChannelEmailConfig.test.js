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
