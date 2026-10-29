import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  createApiToken: vi.fn(),
  createCalendarFeedToken: vi.fn(),
  createMyAgent: vi.fn(),
  deleteMyAgent: vi.fn(),
  getApiTokens: vi.fn(),
  getCalendarFeedToken: vi.fn(),
  getMyAgents: vi.fn(),
  patchCurrentUser: vi.fn(),
  revokeApiToken: vi.fn(),
  revokeCalendarFeedToken: vi.fn(),
  setLocale: vi.fn(),
  updateMyAgent: vi.fn(),
  updateUserAvatar: vi.fn(),
  updateUserRegionalSettings: vi.fn(),
  upload: vi.fn(),
}));

vi.mock('../api/misc.js', () => ({
  attachments: { upload: mocks.upload },
  createCalendarFeedToken: mocks.createCalendarFeedToken,
  getCalendarFeedToken: mocks.getCalendarFeedToken,
  revokeCalendarFeedToken: mocks.revokeCalendarFeedToken,
}))

vi.mock('../api/users.js', () => ({
  createApiToken: mocks.createApiToken,
  createMyAgent: mocks.createMyAgent,
  deleteMyAgent: mocks.deleteMyAgent,
  getApiTokens: mocks.getApiTokens,
  getMyAgents: mocks.getMyAgents,
  revokeApiToken: mocks.revokeApiToken,
  updateMyAgent: mocks.updateMyAgent,
  updateUserAvatar: mocks.updateUserAvatar,
  updateUserRegionalSettings: mocks.updateUserRegionalSettings,
}));

vi.mock('../stores/auth.svelte.js', () => ({
  authStore: { patchCurrentUser: mocks.patchCurrentUser },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  i18n: { locale: 'en', setLocale: mocks.setLocale },
  SUPPORTED_LOCALES: [
    { code: 'en', name: 'English' },
    { code: 'de', name: 'Deutsch' },
  ],
  t: (key) => key,
}));

vi.mock('../composables/useConfirm.js', () => ({ confirm: vi.fn(() => true) }));

import ProfileAgentsTab from './ProfileAgentsTab.svelte';
import ProfileAvatarTab from './ProfileAvatarTab.svelte';
import ProfileCalendarTab from './ProfileCalendarTab.svelte';
import ProfileRegionalSettingsTab from './ProfileRegionalSettingsTab.svelte';
import {
  attachments,
  createCalendarFeedToken,
  getCalendarFeedToken,
  revokeCalendarFeedToken,
} from '../api/misc.js';
import { createApiToken, createMyAgent, deleteMyAgent, getApiTokens, getMyAgents, revokeApiToken, updateMyAgent, updateUserAvatar, updateUserRegionalSettings } from '../api/users.js';


beforeEach(() => {
  vi.clearAllMocks();
  mocks.getMyAgents.mockResolvedValue([]);
  mocks.setLocale.mockResolvedValue(undefined);
});

afterEach(cleanup);

describe('profile tabs', () => {
  it('uploads an avatar and synchronizes the authenticated user', async () => {
    mocks.upload.mockResolvedValue({ success: true, avatar_url: '/avatars/new.png' });
    mocks.updateUserAvatar.mockResolvedValue({
      id: 7,
      full_name: 'Ada Lovelace',
      avatar_url: '/avatars/new.png',
    });
    const { container } = render(ProfileAvatarTab, {
      props: { userId: 7, user: { id: 7, full_name: 'Ada Lovelace' } },
    });

    await fireEvent.click(screen.getByText('users.uploadAvatar'));
    const file = new File(['avatar'], 'avatar.png', { type: 'image/png' });
    await fireEvent.change(container.querySelector('input[type="file"]'), {
      target: { files: [file] },
    });

    await waitFor(() => expect(mocks.updateUserAvatar).toHaveBeenCalledWith(7, '/avatars/new.png'));
    expect(mocks.patchCurrentUser).toHaveBeenCalledWith({ avatar_url: '/avatars/new.png' });
    expect(screen.getByAltText('users.currentProfilePicture')).toHaveAttribute(
      'src',
      '/avatars/new.png'
    );
  });

  it('saves the loaded regional settings and synchronizes locale state', async () => {
    const user = { id: 7, timezone: 'Europe/Zurich', language: 'de' };
    mocks.updateUserRegionalSettings.mockResolvedValue(user);
    render(ProfileRegionalSettingsTab, { props: { userId: 7, user } });

    await fireEvent.click(screen.getByText('users.saveSettings'));

    await waitFor(() =>
      expect(mocks.updateUserRegionalSettings).toHaveBeenCalledWith(7, {
        timezone: 'Europe/Zurich',
        language: 'de',
      })
    );
    expect(mocks.patchCurrentUser).toHaveBeenCalledWith({
      timezone: 'Europe/Zurich',
      language: 'de',
    });
    expect(mocks.setLocale).toHaveBeenCalledWith('de');
    expect(screen.getByText('users.settingsSaved')).toBeInTheDocument();
  });

  it('loads and creates coding agents within the agents tab', async () => {
    mocks.createMyAgent.mockResolvedValue({
      id: 12,
      username: 'build-bot',
      first_name: 'Build',
      last_name: 'Bot',
      full_name: 'Build Bot',
      is_active: true,
    });
    render(ProfileAgentsTab, { props: { userId: 7 } });

    await waitFor(() => expect(mocks.getMyAgents).toHaveBeenCalledOnce());
    await fireEvent.input(screen.getByPlaceholderText('common.username'), {
      target: { value: 'build-bot' },
    });
    await fireEvent.input(screen.getByPlaceholderText('users.firstName'), {
      target: { value: 'Build' },
    });
    await fireEvent.input(screen.getByPlaceholderText('users.lastName'), {
      target: { value: 'Bot' },
    });
    await fireEvent.click(screen.getByText('users.agents.create'));

    await waitFor(() =>
      expect(mocks.createMyAgent).toHaveBeenCalledWith({
        username: 'build-bot',
        first_name: 'Build',
        last_name: 'Bot',
        email: undefined,
      })
    );
    expect(screen.getByTestId('agent-row-12')).toHaveTextContent('Build Bot');
  });

  it('loads and generates a calendar feed URL', async () => {
    mocks.getCalendarFeedToken
      .mockResolvedValueOnce({ has_token: false })
      .mockResolvedValueOnce({
        has_token: true,
        feed: { feed_url: 'https://calendar.example.test/feed/new-token' },
      });
    mocks.createCalendarFeedToken.mockResolvedValue({});
    render(ProfileCalendarTab);

    await fireEvent.click(screen.getByText('users.loadCalendarFeedSettings'));
    await fireEvent.click(await screen.findByText('users.generateCalendarFeedUrl'));

    await waitFor(() => expect(mocks.createCalendarFeedToken).toHaveBeenCalledOnce());
    expect(mocks.getCalendarFeedToken).toHaveBeenCalledTimes(2);
    expect(await screen.findByDisplayValue('https://calendar.example.test/feed/new-token')).toBeVisible();
  });
});
