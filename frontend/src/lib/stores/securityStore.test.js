import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api.js', () => ({
  api: {
    getUser: vi.fn(),
    getUserCredentials: vi.fn(),
    getApiTokens: vi.fn(),
    getScopeCatalog: vi.fn(),
    createApiToken: vi.fn(),
    startFIDORegistration: vi.fn(),
    completeFIDORegistration: vi.fn(),
  },
}));

import { api } from '../api.js';
import { capabilitiesStore } from './capabilities.svelte.js';
import { securityStore } from './securityStore.svelte.js';

describe('securityStore API tokens', () => {
  beforeEach(() => {
    securityStore.reset();
    vi.clearAllMocks();
  });

  it('sends a selected expiration date without converting it to a timestamp', async () => {
    securityStore.newTokenName = 'Automation';
    securityStore.newTokenScopes = ['items:read'];
    securityStore.newTokenExpiry = '2030-06-15';
    api.createApiToken.mockResolvedValue({ token: 'crw_secret' });
    api.getApiTokens.mockResolvedValue([]);

    await securityStore.createApiToken();

    expect(api.createApiToken).toHaveBeenCalledWith({
      name: 'Automation',
      permissions: ['items:read'],
      expires_on: '2030-06-15',
    });
    expect(securityStore.newTokenValue).toBe('crw_secret');
  });

  it('sends null when no expiration date is selected', async () => {
    securityStore.newTokenName = 'Automation';
    securityStore.newTokenScopes = ['items:read'];
    api.createApiToken.mockResolvedValue({ token: 'crw_secret' });
    api.getApiTokens.mockResolvedValue([]);

    await securityStore.createApiToken();

    expect(api.createApiToken).toHaveBeenCalledWith({
      name: 'Automation',
      permissions: ['items:read'],
      expires_on: null,
    });
  });
});

describe('securityStore passkey enrollment mode', () => {
  beforeEach(() => {
    securityStore.reset();
    capabilitiesStore.reset();
    vi.clearAllMocks();
    global.fetch = vi
      .fn()
      .mockResolvedValue({ ok: true, json: async () => ({ ssh_available: true }) });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('does not load unrelated protected resources for a restricted session', () => {
    securityStore.checkEnrollmentRequired('passkey');
    securityStore.setCurrentUserId(42, { enrollmentOnly: true });

    expect(securityStore.enrollmentOnly).toBe(true);
    expect(securityStore.showEnrollmentBanner).toBe(true);
    expect(securityStore.showAddCredential).toBe(true);
    expect(securityStore.credentialType).toBe('fido');
    expect(api.getUser).not.toHaveBeenCalled();
    expect(api.getUserCredentials).not.toHaveBeenCalled();
    expect(api.getApiTokens).not.toHaveBeenCalled();
  });

  it('reuses the shell feature snapshot instead of fetching features again', async () => {
    capabilitiesStore.hydrate({ ssh_available: true, capabilities: [] });

    securityStore.setCurrentUserId(42);

    await vi.waitFor(() => expect(securityStore.sshAvailable).toBe(true));
    expect(global.fetch).not.toHaveBeenCalled();
  });

  it('resets stale state when a different user enters enrollment', () => {
    securityStore.setCurrentUserId(1);
    securityStore.credentials = [{ id: 'old-user-credential' }];
    vi.clearAllMocks();

    securityStore.setCurrentUserId(2, { enrollmentOnly: true });

    expect(securityStore.currentUserId).toBe(2);
    expect(securityStore.credentials).toEqual([]);
    expect(securityStore.enrollmentOnly).toBe(true);
    expect(api.getUser).not.toHaveBeenCalled();
    expect(api.getUserCredentials).not.toHaveBeenCalled();
    expect(api.getApiTokens).not.toHaveBeenCalled();
  });

  it('cannot dismiss or cancel required enrollment', () => {
    securityStore.checkEnrollmentRequired('passkey');
    securityStore.newCredentialName = 'My passkey';

    securityStore.dismissEnrollmentBanner();
    securityStore.resetCredentialForm();

    expect(securityStore.showEnrollmentBanner).toBe(true);
    expect(securityStore.showAddCredential).toBe(true);
    expect(securityStore.newCredentialName).toBe('My passkey');
  });

  it('loads normal security data only after registration elevates the session', async () => {
    securityStore.checkEnrollmentRequired('passkey');
    securityStore.setCurrentUserId(42, { enrollmentOnly: true });
    securityStore.newCredentialName = 'Laptop passkey';

    api.startFIDORegistration.mockResolvedValue({
      sessionId: 'registration-session',
      publicKey: { challenge: 'challenge' },
    });
    api.completeFIDORegistration.mockResolvedValue({ status: 'success' });
    api.getUser.mockResolvedValue({ id: 42, username: 'user' });
    api.getUserCredentials.mockResolvedValue([{ id: 'credential-1' }]);
    api.getApiTokens.mockResolvedValue([]);

    const create = vi.fn().mockResolvedValue({ id: 'browser-credential' });
    vi.stubGlobal('navigator', { credentials: { create } });

    const result = await securityStore.startFIDORegistration(
      (options) => ({ publicKey: options }),
      () => ({ id: 'processed-credential' })
    );

    expect(api.completeFIDORegistration).toHaveBeenCalledWith(42, {
      sessionId: 'registration-session',
      credentialName: 'Laptop passkey',
      response: { id: 'processed-credential' },
    });
    expect(result).toEqual({ success: true, wasEnrollmentRequired: true });
    expect(securityStore.enrollmentOnly).toBe(false);
    expect(securityStore.showEnrollmentBanner).toBe(false);
    expect(securityStore.showAddCredential).toBe(false);
    expect(api.getUser).toHaveBeenCalledTimes(1);
    expect(api.getUserCredentials).toHaveBeenCalledTimes(1);
    expect(api.getApiTokens).toHaveBeenCalledTimes(1);
  });
});
