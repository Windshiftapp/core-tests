import { fireEvent, render, screen } from '@testing-library/svelte';
import { readable } from 'svelte/store';
import { describe, expect, it, vi } from 'vitest';

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('../../stores/permissions.svelte.js', () => ({
  isSystemAdmin: readable(true),
}));

vi.mock('../../api.js', () => ({
  api: { channels: {} },
}));

import ChannelSMTPConfig from './ChannelSMTPConfig.svelte';

describe('ChannelSMTPConfig TLS verification', () => {
  it('stores a certificate verification opt-out only in the SMTP config', async () => {
    const { component } = render(ChannelSMTPConfig, {
      props: { channelId: 7 },
    });

    expect(screen.getByText('channel.smtpSkipTlsVerifyDescription')).toBeInTheDocument();
    expect(component.getConfig().smtp_skip_tls_verify).toBe(false);

    await fireEvent.click(screen.getByTestId('smtp-skip-tls-verify'));

    expect(component.getConfig()).toMatchObject({
      smtp_encryption: 'tls',
      smtp_skip_tls_verify: true,
    });
  });

  it('clears credentials and TLS settings when plaintext SMTP is selected', async () => {
    const formData = {
      host: 'postfix.internal',
      port: 25,
      username: 'relay-user',
      password: 'relay-password',
      from_email: 'windshift@example.com',
      from_name: 'Windshift',
      encryption: 'none',
      skip_tls_verify: true,
      enabled: false,
    };
    const { component } = render(ChannelSMTPConfig, {
      props: { channelId: 7, formData },
    });

    expect(document.querySelector('#smtp-encryption')).toHaveTextContent('channel.noEncryption');
    expect(screen.getByTestId('smtp-plaintext-warning')).toBeInTheDocument();
    expect(screen.queryByTestId('smtp-skip-tls-verify')).not.toBeInTheDocument();
    expect(screen.queryByTestId('smtp-username')).not.toBeInTheDocument();
    expect(screen.queryByTestId('smtp-password')).not.toBeInTheDocument();
    expect(component.getConfig()).toMatchObject({
      smtp_encryption: 'none',
      smtp_username: '',
      smtp_password: '',
      smtp_skip_tls_verify: false,
    });
  });
});
