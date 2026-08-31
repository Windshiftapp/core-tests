import { get } from 'svelte/store';
import { beforeEach, describe, expect, it, vi } from 'vitest';

// Mock the api module before importing authStore
vi.mock('../api.js', () => ({
  api: {
    auth: {
      getCurrentUser: vi.fn(),
      login: vi.fn(),
      logout: vi.fn(),
      logoutAll: vi.fn(),
      refreshSession: vi.fn(),
      changePassword: vi.fn(),
    },
  },
}));

vi.mock('../api/core.js', () => ({
  setAPIRequestSessionKey: vi.fn(),
}));

import { setAPIRequestSessionKey } from '../api/core.js';
import { api } from '../api.js';
// Import after mocking
import { authStore } from './auth.svelte.js';

describe('authStore', () => {
  beforeEach(() => {
    // Reset store state before each test
    authStore.clearAuth();
    vi.clearAllMocks();
  });

  describe('init()', () => {
    it('should fetch user and set authenticated state on success', async () => {
      const mockUser = { id: '1', username: 'testuser', email: 'test@example.com' };
      const mockSession = { id: 'session-1', expires_at: '2025-01-01T00:00:00Z' };

      api.auth.getCurrentUser.mockResolvedValueOnce({
        user: mockUser,
        session: mockSession,
      });

      await authStore.init();

      const state = get(authStore);
      expect(state.isAuthenticated).toBe(true);
      expect(state.user).toEqual(mockUser);
      expect(state.session).toEqual(mockSession);
      expect(state.loading).toBe(false);
      expect(state.error).toBeNull();
      expect(api.auth.getCurrentUser).toHaveBeenCalledTimes(1);
    });

    it('does not authenticate a refreshed password+passkey session before assertion', async () => {
      api.auth.getCurrentUser.mockResolvedValueOnce({
        user: { id: '1', username: 'pending-user' },
        session: {
          enrollment_required: true,
          auth_pending_type: 'passkey_verification',
        },
      });

      await authStore.init();

      expect(get(authStore)).toMatchObject({
        isAuthenticated: false,
        user: null,
        session: null,
        loading: false,
        error: null,
      });
    });

    it('sets unauthenticated state only for an explicit 401', async () => {
      const unauthorized = new Error('Unauthorized');
      unauthorized.status = 401;
      api.auth.getCurrentUser.mockRejectedValueOnce(unauthorized);

      const result = await authStore.init();

      const state = get(authStore);
      expect(state.isAuthenticated).toBe(false);
      expect(state.user).toBeNull();
      expect(state.session).toBeNull();
      expect(state.loading).toBe(false);
      expect(state.error).toBeNull();
      expect(result).toEqual({ status: 'unauthenticated' });
    });

    it('preserves prior auth and returns a retryable transport error', async () => {
      const priorUser = { id: '1', username: 'testuser' };
      const priorSession = { id: 'session-1' };
      const networkError = Object.assign(new Error('Unable to connect'), {
        status: 0,
        code: 'NETWORK_ERROR',
      });
      authStore.setAuthData(priorUser, priorSession);
      api.auth.getCurrentUser.mockRejectedValueOnce(networkError);

      const result = await authStore.init({ timeout: 10_000 });

      expect(get(authStore)).toMatchObject({
        isAuthenticated: true,
        user: priorUser,
        session: priorSession,
        loading: false,
        error: 'Unable to connect',
      });
      expect(result).toEqual({ status: 'error', error: networkError });
      expect(api.auth.getCurrentUser).toHaveBeenCalledWith({ timeout: 10_000 });
    });
  });

  describe('login()', () => {
    it('should update user and session on successful login', async () => {
      const mockUser = { id: '1', username: 'testuser' };
      const mockSession = { id: 'session-1' };

      api.auth.login.mockResolvedValueOnce({
        success: true,
        user: mockUser,
      });
      api.auth.getCurrentUser.mockResolvedValueOnce({
        user: mockUser,
        session: mockSession,
      });

      const result = await authStore.login({ username: 'testuser', password: 'password123' });

      expect(result.success).toBe(true);
      const state = get(authStore);
      expect(state.isAuthenticated).toBe(true);
      expect(state.user).toEqual(mockUser);
      expect(state.session).toEqual(mockSession);
      expect(state.loading).toBe(false);
      expect(state.error).toBeNull();
    });

    it('should handle invalid credentials', async () => {
      api.auth.login.mockResolvedValueOnce({
        success: false,
        message: 'Invalid username or password',
      });

      const result = await authStore.login({ username: 'wrong', password: 'wrong' });

      expect(result.success).toBe(false);
      expect(result.message).toBe('Invalid username or password');
      const state = get(authStore);
      expect(state.isAuthenticated).toBe(false);
      expect(state.error).toBe('Invalid username or password');
      expect(state.loading).toBe(false);
    });

    it('should clear auth state if session fetch fails after successful login', async () => {
      authStore.setAuthData({ id: 'old-user' }, { id: 'old-session' });
      api.auth.login.mockResolvedValueOnce({
        success: true,
        user: { id: '1', username: 'testuser' },
      });
      api.auth.getCurrentUser.mockRejectedValueOnce(new Error('Session fetch failed'));

      const result = await authStore.login({ username: 'testuser', password: 'password123' });

      expect(result.success).toBe(false);
      expect(result.message).toBe('Session fetch failed');
      const state = get(authStore);
      expect(state.isAuthenticated).toBe(false);
      expect(state.user).toBeNull();
      expect(state.session).toBeNull();
      expect(state.loading).toBe(false);
      expect(state.error).toBe('Session fetch failed');
    });

    it('should handle login API errors', async () => {
      api.auth.login.mockRejectedValueOnce(new Error('Network error'));

      const result = await authStore.login({ username: 'user', password: 'pass' });

      expect(result.success).toBe(false);
      expect(result.message).toBe('Network error');
      const state = get(authStore);
      expect(state.isAuthenticated).toBe(false);
      expect(state.error).toBe('Network error');
    });

    it('keeps password+passkey login pending until WebAuthn completes', async () => {
      api.auth.login.mockResolvedValueOnce({
        success: false,
        passkey_required: true,
        policy_message: 'Verify your passkey',
      });

      const result = await authStore.login({ username: 'user', password: 'pass' });

      expect(result).toEqual({
        success: false,
        passkey_required: true,
        policy_message: 'Verify your passkey',
      });
      expect(get(authStore).isAuthenticated).toBe(false);
      expect(api.auth.getCurrentUser).not.toHaveBeenCalled();
    });

    it('recognizes passkey-only policy fields from an HTTP error body', async () => {
      const policyError = Object.assign(new Error('Password login disabled'), {
        body: { passkey_required: true, policy_message: 'Use a passkey' },
      });
      api.auth.login.mockRejectedValueOnce(policyError);

      const result = await authStore.login({ username: 'user', password: 'pass' });

      expect(result.passkey_required).toBe(true);
      expect(result.policy_message).toBe('Use a passkey');
      expect(get(authStore).isAuthenticated).toBe(false);
    });
  });

  describe('completePasskeyLogin()', () => {
    it('loads canonical session metadata before authenticating', async () => {
      const mockUser = { id: '1', username: 'passkey-user' };
      const mockSession = { id: 'session-1', expires_at: '2026-01-01T00:00:00Z' };
      api.auth.getCurrentUser.mockResolvedValueOnce({ user: mockUser, session: mockSession });

      await expect(authStore.completePasskeyLogin()).resolves.toBe(true);

      expect(get(authStore)).toMatchObject({
        user: mockUser,
        session: mockSession,
        isAuthenticated: true,
        loading: false,
        error: null,
      });
    });

    it('does not authenticate when session elevation cannot be confirmed', async () => {
      api.auth.getCurrentUser.mockRejectedValueOnce(new Error('Still pending'));

      await expect(authStore.completePasskeyLogin({ id: 'fallback' })).rejects.toThrow(
        'Still pending'
      );
      expect(get(authStore).isAuthenticated).toBe(false);
      expect(get(authStore).session).toBeNull();
    });
  });

  describe('logout()', () => {
    it('should clear state on successful logout', async () => {
      // First set up authenticated state
      const mockUser = { id: '1', username: 'testuser' };
      authStore.setAuthData(mockUser, { id: 'session-1' });

      api.auth.logout.mockResolvedValueOnce({});

      await authStore.logout();

      const state = get(authStore);
      expect(state.isAuthenticated).toBe(false);
      expect(state.user).toBeNull();
      expect(state.session).toBeNull();
      expect(state.loading).toBe(false);
      expect(api.auth.logout).toHaveBeenCalledTimes(1);
    });

    it('should clear state even if API call fails', async () => {
      // First set up authenticated state
      const mockUser = { id: '1', username: 'testuser' };
      authStore.setAuthData(mockUser, { id: 'session-1' });

      // Mock console.warn to verify it's called
      const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
      api.auth.logout.mockRejectedValueOnce(new Error('Network error'));

      await authStore.logout();

      const state = get(authStore);
      expect(state.isAuthenticated).toBe(false);
      expect(state.user).toBeNull();
      expect(state.session).toBeNull();
      expect(warnSpy).toHaveBeenCalledWith('Logout API call failed:', expect.any(Error));

      warnSpy.mockRestore();
    });
  });

  describe('clearAuth()', () => {
    it('should reset all stores and set session expired error', () => {
      // First set up authenticated state
      const mockUser = { id: '1', username: 'testuser' };
      authStore.setAuthData(mockUser, { id: 'session-1' });

      // Verify we're authenticated
      expect(get(authStore).isAuthenticated).toBe(true);

      // Clear auth (simulating session expiry)
      authStore.clearAuth();

      const state = get(authStore);
      expect(state.isAuthenticated).toBe(false);
      expect(state.user).toBeNull();
      expect(state.session).toBeNull();
      expect(state.loading).toBe(false);
      expect(state.error).toBe('Session expired. Please log in again.');
    });
  });

  describe('setAuthData()', () => {
    it('should set user and session directly (for FIDO login)', () => {
      const mockUser = { id: '1', username: 'fidouser' };
      const mockSession = { id: 'fido-session-1' };

      authStore.setAuthData(mockUser, mockSession);

      const state = get(authStore);
      expect(state.isAuthenticated).toBe(true);
      expect(state.user).toEqual(mockUser);
      expect(state.session).toEqual(mockSession);
      expect(state.loading).toBe(false);
      expect(state.error).toBeNull();
    });

    it('scopes shared GET ownership to auth and clears it on logout state', () => {
      authStore.setAuthData({ id: '7' }, { id: 'session-7' });
      expect(setAPIRequestSessionKey).toHaveBeenLastCalledWith('auth:7:session-7');

      authStore.clearAuth();
      expect(setAPIRequestSessionKey).toHaveBeenLastCalledWith(null);
    });
  });

  describe('patchCurrentUser()', () => {
    it('updates profile data without replacing the active session', () => {
      authStore.setAuthData(
        { id: '7', username: 'avatar-user', avatar_url: '' },
        { id: 'session-7' }
      );

      authStore.patchCurrentUser({ avatar_url: '/api/attachments/9/download' });

      expect(get(authStore)).toMatchObject({
        user: { id: '7', avatar_url: '/api/attachments/9/download' },
        session: { id: 'session-7' },
        isAuthenticated: true,
      });
    });
  });

  describe('refreshSession()', () => {
    it('should update session on successful refresh', async () => {
      const mockSession = { id: 'refreshed-session', expires_at: '2025-02-01T00:00:00Z' };

      api.auth.refreshSession.mockResolvedValueOnce({});
      api.auth.getCurrentUser.mockResolvedValueOnce({
        user: { id: '1' },
        session: mockSession,
      });

      const result = await authStore.refreshSession(true);

      expect(result).toBe(true);
      expect(api.auth.refreshSession).toHaveBeenCalledWith({ remember_me: true });
      const state = get(authStore);
      expect(state.session).toEqual(mockSession);
    });

    it('should return false on refresh failure', async () => {
      const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
      api.auth.refreshSession.mockRejectedValueOnce(new Error('Session invalid'));

      const result = await authStore.refreshSession();

      expect(result).toBe(false);
      warnSpy.mockRestore();
    });
  });

  describe('changePassword()', () => {
    it('should return success on password change', async () => {
      api.auth.changePassword.mockResolvedValueOnce({
        message: 'Password updated successfully',
      });

      const result = await authStore.changePassword({
        current_password: 'oldpass',
        new_password: 'newpass',
      });

      expect(result.success).toBe(true);
      expect(result.message).toBe('Password updated successfully');
      const state = get(authStore);
      expect(state.loading).toBe(false);
      expect(state.error).toBeNull();
    });

    it('should return failure on password change error', async () => {
      api.auth.changePassword.mockRejectedValueOnce(new Error('Current password is incorrect'));

      const result = await authStore.changePassword({
        current_password: 'wrongpass',
        new_password: 'newpass',
      });

      expect(result.success).toBe(false);
      expect(result.message).toBe('Current password is incorrect');
      const state = get(authStore);
      expect(state.error).toBe('Current password is incorrect');
    });
  });

  describe('convenience getters', () => {
    it('should provide currentUser getter', () => {
      const mockUser = { id: '1', username: 'testuser' };
      authStore.setAuthData(mockUser, { id: 'session-1' });

      expect(authStore.currentUser).toEqual(mockUser);
    });

    it('should provide isAuthenticated getter', () => {
      expect(authStore.isAuthenticated).toBe(false);

      authStore.setAuthData({ id: '1' }, { id: 'session-1' });

      expect(authStore.isAuthenticated).toBe(true);
    });
  });
});
