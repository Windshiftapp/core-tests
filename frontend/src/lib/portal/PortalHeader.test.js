import { fireEvent, render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../stores', async () => {
  const { writable } = await import('svelte/store');
  const authStore = writable({ isAuthenticated: false, currentUser: null });
  authStore.logout = vi.fn();
  return { authStore };
});

vi.mock('../stores/portalAuth.svelte.js', async () => {
  const { writable } = await import('svelte/store');
  const portalAuthStore = writable({
    isAuthenticated: false,
    isInternal: false,
    customer: null,
    user: null,
  });
  portalAuthStore.logout = vi.fn();
  portalAuthStore.reset = vi.fn();
  return { portalAuthStore };
});

vi.mock('../stores/portal.svelte.js', () => ({
  portalStore: {
    hasBackgroundImage: false,
    hasGradient: false,
    headerBackgroundStyle: '',
    openRequestCount: 0,
    pendingApprovalCount: 0,
    showProfileMenu: false,
    showMainMenu: false,
    currentSlug: 'support',
    isDarkMode: false,
    portalData: { title: 'Support' },
    toggleTheme: vi.fn(),
    setShowMyRequests: vi.fn(),
    setShowMyApprovals: vi.fn(),
    setShowMyDrafts: vi.fn(),
    toggleMyRequests: vi.fn(),
    toggleMyApprovals: vi.fn(),
    toggleMyDrafts: vi.fn(),
  },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('../router.js', () => ({ navigate: vi.fn() }));

import { authStore } from '../stores';
import { portalAuthStore } from '../stores/portalAuth.svelte.js';
import PortalHeader from './PortalHeader.svelte';

describe('PortalHeader', () => {
  beforeEach(() => {
    authStore.set({ isAuthenticated: false, currentUser: null });
    portalAuthStore.set({
      isAuthenticated: false,
      isInternal: false,
      customer: null,
      user: null,
    });
  });

  it('uses the current internal user avatar when the portal snapshot is stale', () => {
    authStore.set({
      isAuthenticated: true,
      currentUser: {
        id: 7,
        email: 'admin@example.test',
        first_name: 'Portal',
        last_name: 'Admin',
        avatar_url: '/api/attachments/99/download',
      },
    });
    portalAuthStore.set({
      isAuthenticated: true,
      isInternal: true,
      customer: null,
      user: {
        id: 7,
        name: 'Portal Admin',
        email: 'admin@example.test',
        avatar_url: '',
      },
    });

    render(PortalHeader);

    expect(screen.getByTestId('portal-user-avatar')).toHaveAttribute(
      'src',
      '/api/attachments/99/download'
    );
  });

  it('shows an internal user avatar in the portal account button', () => {
    portalAuthStore.set({
      isAuthenticated: true,
      isInternal: true,
      customer: null,
      user: {
        id: 7,
        name: 'Portal Admin',
        email: 'admin@example.test',
        avatar_url: '/api/attachments/42/download',
      },
    });

    render(PortalHeader);

    expect(screen.getByTestId('portal-user-avatar')).toHaveAttribute(
      'src',
      '/api/attachments/42/download'
    );
  });

  it('shows the internal user initials when no avatar picture exists', () => {
    portalAuthStore.set({
      isAuthenticated: true,
      isInternal: true,
      customer: null,
      user: {
        id: 7,
        name: 'Portal Admin',
        email: 'admin@example.test',
        avatar_url: '',
      },
    });

    render(PortalHeader);

    expect(screen.queryByTestId('portal-user-avatar')).not.toBeInTheDocument();
    expect(screen.getByTestId('portal-user-avatar-fallback')).toHaveTextContent('PA');
  });

  it('shows the customer initials when no avatar picture exists', () => {
    portalAuthStore.set({
      isAuthenticated: true,
      isInternal: false,
      customer: {
        name: 'Jane Cooper',
        email: 'jane@example.test',
      },
      user: null,
    });

    render(PortalHeader);

    expect(screen.queryByTestId('portal-user-avatar')).not.toBeInTheDocument();
    expect(screen.getByTestId('portal-user-avatar-fallback')).toHaveTextContent('JC');
  });

  it('keeps the fallback visible when the avatar image cannot load', async () => {
    portalAuthStore.set({
      isAuthenticated: true,
      isInternal: true,
      customer: null,
      user: {
        id: 7,
        name: 'Portal Admin',
        email: 'admin@example.test',
        avatar_url: '/api/attachments/missing/download',
      },
    });

    render(PortalHeader);

    const avatar = screen.getByTestId('portal-user-avatar');
    expect(screen.getByTestId('portal-user-avatar-fallback')).toBeVisible();

    await fireEvent.error(avatar);

    expect(screen.queryByTestId('portal-user-avatar')).not.toBeInTheDocument();
    expect(screen.getByTestId('portal-user-avatar-fallback')).toBeVisible();
  });
});
