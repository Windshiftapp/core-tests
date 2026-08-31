import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  getAuthPolicy: vi.fn(),
  getSecuritySettings: vi.fn(),
  updateSecuritySettings: vi.fn(),
}));

vi.mock('../api.js', () => ({
  agentSecurity: {
    getSettings: vi.fn().mockResolvedValue({ allow_centralized_service_users: false }),
    updateSettings: vi.fn(),
    listAllowlist: vi.fn().mockResolvedValue([]),
    addAllowlist: vi.fn(),
    removeAllowlist: vi.fn(),
  },
  api: {
    getUsers: vi.fn().mockResolvedValue([]),
    workspaces: { getAll: vi.fn().mockResolvedValue([]) },
  },
  getSecuritySettings: mocks.getSecuritySettings,
  updateSecuritySettings: mocks.updateSecuritySettings,
  authPolicy: {
    get: mocks.getAuthPolicy,
    update: vi.fn(),
    getStats: vi.fn().mockResolvedValue(null),
    getAffected: vi.fn().mockResolvedValue([]),
  },
}));

vi.mock('../stores/i18n.svelte.js', async (importOriginal) => ({
  ...(await importOriginal()),
  t: (key) => key,
}));

vi.mock('../stores/toasts.svelte.js', () => ({
  errorToast: vi.fn(),
}));

import SecuritySettings from './SecuritySettings.svelte';

describe('SecuritySettings external images', () => {
  beforeEach(() => {
    mocks.getAuthPolicy.mockResolvedValue({
      policy: 'password',
      preview_mode: false,
      sso_configured: false,
      fallback_enabled: false,
      hide_password_form: false,
    });
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
});
