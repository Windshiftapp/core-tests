import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  getUserBootstrap: vi.fn(),
  getCurrentCustomer: vi.fn(),
}));

vi.mock('../api.js', () => ({
  api: {
    portal: { getUserBootstrap: mocks.getUserBootstrap },
    portalAuth: { getCurrentCustomer: mocks.getCurrentCustomer },
    portalPasskey: {},
  },
}));

vi.mock('../utils/webauthn-utils.js', () => ({
  isWebAuthnSupported: vi.fn(() => false),
  prepareCredentialRequestOptions: vi.fn(),
  processCredentialRequestResponse: vi.fn(),
}));

const { portalAuthStore } = await import('./portalAuth.svelte.js');

describe('portal auth cold bootstrap', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    portalAuthStore.reset();
  });

  it('hydrates authentication and badge data with one user-bootstrap request', async () => {
    const response = {
      authenticated: true,
      is_internal: true,
      user: { id: 4 },
      my_requests: [{ id: 10 }],
      my_approvals: [{ id: 11 }],
    };
    mocks.getUserBootstrap.mockResolvedValue(response);

    await expect(portalAuthStore.checkAuth('support')).resolves.toBe(response);

    expect(mocks.getUserBootstrap).toHaveBeenCalledOnce();
    expect(mocks.getUserBootstrap).toHaveBeenCalledWith('support');
    expect(mocks.getCurrentCustomer).not.toHaveBeenCalled();
    expect(portalAuthStore.isAuthenticated).toBe(true);
    expect(portalAuthStore.isInternal).toBe(true);
  });
});
