import { cleanup, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ getUser: vi.fn() }));
const stores = vi.hoisted(() => ({
  attachmentStatus: { enabled: true },
  authStore: { currentUser: { id: 7 } },
}));

vi.mock('../api/users.js', () => ({
  getUser: mocks.getUser,
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
vi.mock('../profile/ProfileRegionalSettingsTab.svelte', () => ({
  default: function ProfileRegionalSettingsTab() {},
}));

import UserProfile from './UserProfile.svelte';
import { getUser } from '../api/users.js';

describe('UserProfile', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    stores.attachmentStatus.enabled = true;
    mocks.getUser.mockResolvedValue({
      id: 7,
      full_name: 'Ada Lovelace',
      email: 'ada@example.test',
    });
  });

  afterEach(cleanup);

  it('loads the authenticated user once during initial rendering', async () => {
    render(UserProfile);

    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument();
    expect(screen.getByText('ada@example.test')).toBeInTheDocument();
    await waitFor(() => expect(mocks.getUser).toHaveBeenCalledOnce());
    expect(mocks.getUser).toHaveBeenCalledWith(7);
  });

  it('omits the avatar tab when attachments are unavailable', async () => {
    stores.attachmentStatus.enabled = false;
    render(UserProfile);

    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument();
    expect(screen.queryByText('users.avatar')).not.toBeInTheDocument();
    expect(screen.getByText('users.regionalSettings')).toBeVisible();
  });
});
