import { cleanup, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ getMyProfile: vi.fn() }));
const stores = vi.hoisted(() => ({
  attachmentStatus: { enabled: true },
  authStore: { currentUser: { id: 7 } },
}));

vi.mock('../api/users.js', () => ({
  getMyProfile: mocks.getMyProfile,
}));

vi.mock('../stores/attachmentStatus.svelte.js', () => ({
  attachmentStatus: stores.attachmentStatus,
}));
vi.mock('../stores/auth.svelte.js', () => ({ authStore: stores.authStore }));

vi.mock('../stores/i18n.svelte.js', () => ({ t: (key) => key }));

vi.mock('../profile/ProfileAvatarTab.svelte', () => ({
  default: function ProfileAvatarTab() {},
}));
vi.mock('../profile/ProfileAgentsTab.svelte', () => ({
  default: function ProfileAgentsTab() {},
}));
vi.mock('../profile/ProfileCalendarTab.svelte', () => ({
  default: function ProfileCalendarTab() {},
}));
vi.mock('../profile/ProfileRegionalSettingsTab.svelte', async () => ({
  default: (await import('../profile/ProfileRegionalSettingsTabStub.svelte')).default,
}));

import UserProfile from './UserProfile.svelte';
import { getMyProfile } from '../api/users.js';

describe('UserProfile', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    stores.attachmentStatus.enabled = true;
    mocks.getMyProfile.mockResolvedValue({
      id: 7,
      full_name: 'Ada Lovelace',
      email: 'ada@example.test',
      timezone: 'Europe/Zurich',
      language: 'de',
      requires_password_reset: true,
    });
  });

  afterEach(cleanup);

  it('loads the authenticated user once during initial rendering', async () => {
    render(UserProfile);

    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument();
    expect(screen.getByText('ada@example.test')).toBeInTheDocument();
    expect(screen.getByText('users.passwordResetRequired')).toBeInTheDocument();
    await waitFor(() => expect(mocks.getMyProfile).toHaveBeenCalledOnce());
  });

  it('passes the loaded private profile fields to the regional settings tab', async () => {
    render(UserProfile);

    expect(await screen.findByTestId('regional-settings-language')).toHaveTextContent('de');
    expect(screen.getByTestId('regional-settings-timezone')).toHaveTextContent('Europe/Zurich');
  });

  it('omits the avatar tab when attachments are unavailable', async () => {
    stores.attachmentStatus.enabled = false;
    render(UserProfile);

    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument();
    expect(screen.queryByText('users.avatar')).not.toBeInTheDocument();
    expect(screen.getByText('users.regionalSettings')).toBeVisible();
  });
});
