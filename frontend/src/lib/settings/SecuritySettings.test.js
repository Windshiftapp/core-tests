import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  getAuthPolicy: vi.fn(),
  getSecuritySettings: vi.fn(),
  updateSecuritySettings: vi.fn(),
}));

vi.mock('../api/agentSecurity.js', () => ({
  agentSecurity: {
    getSettings: vi.fn().mockResolvedValue({ allow_centralized_service_users: false }),
    updateSettings: vi.fn(),
    listAllowlist: vi.fn().mockResolvedValue([]),
    addAllowlist: vi.fn(),
    removeAllowlist: vi.fn(),
  },
}));

vi.mock('../api/admin.js', () => ({
  getSecuritySettings: mocks.getSecuritySettings,
  updateSecuritySettings: mocks.updateSecuritySettings,
  authPolicy: {
    get: mocks.getAuthPolicy,
    update: vi.fn(),
    getStats: vi.fn().mockResolvedValue(null),
    getAffected: vi.fn().mockResolvedValue([]),
  },
}));

vi.mock('../api/users.js', () => ({
  getUsers: vi.fn().mockResolvedValue([]),
}))

vi.mock('../api/workspaces.js', () => ({
  workspaces: { getAll: vi.fn().mockResolvedValue([]) },
}));

vi.mock('../stores/i18n.svelte.js', async (importOriginal) => {
  const translations = {
    'securitySettings.authenticationMethod': 'Authentication Method',
    'securitySettings.policy.password': 'Password only',
    'securitySettings.policy.passwordDescription': 'Standard password authentication.',
    'securitySettings.policy.passwordOrSso': 'Password or SSO',
    'securitySettings.policy.passwordOrSsoDescription':
      'Users may sign in with a password or through the configured SSO provider.',
  };
  return {
    ...(await importOriginal()),
    t: (key) => translations[key] ?? key,
  };
});

vi.mock('../stores/toasts.svelte.js', () => ({
  errorToast: vi.fn(),
}));

import SecuritySettings from './SecuritySettings.svelte';
import { getUsers } from '../api/users.js';
import { workspaces } from '../api/workspaces.js';


function authPolicyConfig(ssoConfigured) {
  return {
    policy: 'password',
    preview_mode: false,
    sso_configured: ssoConfigured,
    fallback_enabled: false,
    hide_password_form: false,
  };
}

describe('SecuritySettings authentication policy labels', () => {
  beforeEach(() => {
    mocks.getAuthPolicy.mockResolvedValue(authPolicyConfig(false));
    mocks.getSecuritySettings.mockResolvedValue({});
    mocks.updateSecuritySettings.mockResolvedValue({});
  });

  it('keeps external Markdown images off by default and persists an explicit opt-in', async () => {
    render(SecuritySettings);

    const toggle = await screen.findByRole('switch', {
      name: 'settings.security.externalImages',
    });
    expect(toggle).not.toBeChecked();

    await fireEvent.click(toggle);

    await waitFor(() => {
      expect(mocks.updateSecuritySettings).toHaveBeenCalledWith(
        expect.objectContaining({ allow_external_images: true })
      );
    });
    expect(toggle).toBeChecked();
    expect(screen.getByText('settings.security.externalImagesWarning')).toBeInTheDocument();
  });

  it('labels the default policy as password or SSO when SSO is configured', async () => {
    mocks.getAuthPolicy.mockResolvedValue(authPolicyConfig(true));

    render(SecuritySettings);

    expect(
      await screen.findByRole('combobox', { name: 'Authentication Method' })
    ).toHaveTextContent('Password or SSO');
    expect(
      screen.getByText('Users may sign in with a password or through the configured SSO provider.')
    ).toBeInTheDocument();
  });

  it('keeps the password-only label when no SSO provider is configured', async () => {
    render(SecuritySettings);

    expect(
      await screen.findByRole('combobox', { name: 'Authentication Method' })
    ).toHaveTextContent('Password only');
    expect(screen.getByText('Standard password authentication.')).toBeInTheDocument();
  });
});
