import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, test, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  clearError: vi.fn(),
  getPublicStatus: vi.fn(),
  initStatus: vi.fn(),
  startLogin: vi.fn(),
}));

vi.mock('../stores/auth.svelte.js', async () => {
  const { writable } = await import('svelte/store');
  const authStore = Object.assign(writable({ loading: false, error: null }), {
    clearError: mocks.clearError,
    login: vi.fn(),
  });
  const ssoStore = Object.assign(
    writable({
      enabled: true,
      providerName: 'Acme SSO',
      providers: [{ slug: 'acme', name: 'Acme SSO', provider_type: 'oidc' }],
      statusLoading: false,
    }),
    {
      initStatus: mocks.initStatus,
      checkForError: vi.fn().mockReturnValue(null),
      startLogin: mocks.startLogin,
    }
  );
  return {
    authStore,
  };
})

vi.mock('../stores/sso.svelte.js', async () => {
  const { writable } = await import('svelte/store');
  const authStore = Object.assign(writable({ loading: false, error: null }), {
    clearError: mocks.clearError,
    login: vi.fn(),
  });
  const ssoStore = Object.assign(
    writable({
      enabled: true,
      providerName: 'Acme SSO',
      providers: [{ slug: 'acme', name: 'Acme SSO', provider_type: 'oidc' }],
      statusLoading: false,
    }),
    {
      initStatus: mocks.initStatus,
      checkForError: vi.fn().mockReturnValue(null),
      startLogin: mocks.startLogin,
    }
  );
  return {
    ssoStore,
  };
});

;
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
  t: (key, params = {}) => {
    if (key === 'auth.staySignedIn') return 'Keep me signed in to Windshift for 30 days';
    if (key === 'auth.continueWith') return `Continue with ${params.provider}`;
    return key;
  },
}));

import LoginDialog from './LoginDialog.svelte';
import { authPolicy } from '../api/admin.js';

beforeEach(() => {
  mocks.clearError.mockClear();
  mocks.startLogin.mockClear();
  mocks.initStatus.mockReset().mockResolvedValue(undefined);
  mocks.getPublicStatus.mockReset().mockResolvedValue({
    hide_password_form: true,
    sso_enabled: true,
    passkey_required: false,
  });
});

describe('LoginDialog SSO remember-me', () => {
  test('keeps the password form unavailable until login options finish loading', async () => {
    let resolvePolicy;
    let resolveSSO;
    mocks.getPublicStatus.mockReturnValue(
      new Promise((resolve) => {
        resolvePolicy = resolve;
      })
    );
    mocks.initStatus.mockReturnValue(
      new Promise((resolve) => {
        resolveSSO = resolve;
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
    resolveSSO();

    await waitFor(() => expect(screen.getByTestId('login-password-form')).toBeInTheDocument());
  });

  test('shows the 30-day choice before SSO and forwards it in SSO-only mode', async () => {
    render(LoginDialog, { props: { isOpen: true } });

    const rememberMe = await screen.findByRole('checkbox', {
      name: 'Keep me signed in to Windshift for 30 days',
    });
    const ssoButton = screen.getByRole('button', { name: 'Continue with Acme SSO' });

    expect(
      rememberMe.compareDocumentPosition(ssoButton) & Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
    await waitFor(() => expect(screen.getByText('Password login is disabled')).toBeInTheDocument());

    await fireEvent.click(rememberMe);
    await fireEvent.click(ssoButton);

    expect(mocks.startLogin).toHaveBeenCalledWith(true);
  });
});
