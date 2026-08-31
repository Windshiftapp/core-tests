import { render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, test, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  clearError: vi.fn(),
  getPublicStatus: vi.fn(),
  initStatus: vi.fn(),
}));

vi.mock('../stores', async () => {
  const { writable } = await import('svelte/store');
  const authStore = Object.assign(writable({ loading: false, error: null }), {
    clearError: mocks.clearError,
    login: vi.fn(),
  });
  const ssoStore = Object.assign(
    writable({ enabled: false, providers: [], statusLoading: false }),
    {
      initStatus: mocks.initStatus,
      checkForError: vi.fn().mockReturnValue(null),
      startLogin: vi.fn(),
    }
  );
  return { authStore, ssoStore };
});

vi.mock('../api.js', () => ({ api: {} }));
vi.mock('../api/admin.js', () => ({
  authPolicy: { getPublicStatus: mocks.getPublicStatus },
}));
vi.mock('../router.js', () => ({ navigate: vi.fn() }));
vi.mock('../utils/webauthn-utils.js', () => ({
  isWebAuthnSupported: vi.fn().mockReturnValue(false),
}));
vi.mock('../utils/loginUtils.js', () => ({
  deriveFidoError: vi.fn(),
  evaluateFidoAvailability: vi.fn().mockResolvedValue({ available: false, showOption: false }),
  getBaseLoginState: vi.fn().mockReturnValue({
    emailOrUsername: '',
    password: '',
    rememberMe: false,
    showPassword: false,
    validationError: '',
    fidoAvailable: false,
    tryingFido: false,
    showFidoOption: false,
  }),
  performFidoLogin: vi.fn(),
}));
vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

import LoginDialog from './LoginDialog.svelte';

beforeEach(() => {
  mocks.clearError.mockClear();
  mocks.initStatus.mockReset().mockResolvedValue(undefined);
  mocks.getPublicStatus.mockReset().mockResolvedValue({
    hide_password_form: false,
    sso_enabled: false,
    passkey_required: false,
  });
});

describe('LoginDialog option loading', () => {
  test('keeps the password form unavailable until login options finish loading', async () => {
    let resolvePolicy;
    let resolveOptions;
    mocks.getPublicStatus.mockReturnValue(
      new Promise((resolve) => {
        resolvePolicy = resolve;
      })
    );
    mocks.initStatus.mockReturnValue(
      new Promise((resolve) => {
        resolveOptions = resolve;
      })
    );

    render(LoginDialog, { props: { isOpen: true } });

    expect(screen.getByTestId('login-options-loading')).toBeInTheDocument();
    expect(document.querySelector('#password')).not.toBeInTheDocument();

    resolvePolicy({
      hide_password_form: false,
      sso_enabled: false,
      passkey_required: false,
    });
    resolveOptions();

    await waitFor(() => expect(screen.getByTestId('login-password-form')).toBeInTheDocument());
  });
});
