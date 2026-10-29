import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { tick } from 'svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

// The view reads the signed-in user straight off the authStore getter and
// mirrors the saved language back into it after the profile update.
const authState = vi.hoisted(() => ({
  user: { id: 42, timezone: 'Europe/Berlin', language: 'ko' },
}));

vi.mock('../stores/auth.svelte.js', () => ({
  authStore: {
      get currentUser() {
        return authState.user;
      },
      patchCurrentUser(updates) {
        authState.user = { ...authState.user, ...updates };
      },
    },
}));

vi.mock('../stores/toasts.svelte.js', () => ({
  errorToast: vi.fn(),
  infoToast: vi.fn(),
  successToast: vi.fn(),
}));

vi.mock('../router.js', async () => {
  const { writable } = await import('svelte/store');
  return {
    currentRoute: writable({ path: '/m/settings', view: 'mobile-settings', params: {}, query: {} }),
    navigate: vi.fn(),
  };
});

// Only the regional-settings endpoint is under test; the real reactive i18n
// store and locale catalogs stay live so language switches are observable.
vi.mock('../api/users.js', () => ({
  updateUserRegionalSettings: vi.fn(),
}));

import { updateUserRegionalSettings } from '../api/users.js';
import { errorToast } from '../stores/toasts.svelte.js';
import { i18n, SUPPORTED_LOCALES, t } from '../stores/i18n.svelte.js';
import MobileSettingsView from './MobileSettingsView.svelte';

async function switchLocale(locale) {
  await i18n.setLocale(locale);
  await tick();
}

beforeEach(async () => {
  authState.user = { id: 42, timezone: 'Europe/Berlin', language: 'ko' };
  updateUserRegionalSettings.mockReset().mockResolvedValue({ language: 'ru', timezone: 'Europe/Berlin' });
  await switchLocale('ko');
});

afterEach(() => {
  document.body.innerHTML = '';
  vi.clearAllMocks();
});

describe('MobileSettingsView language switching', () => {
  test('renders localized settings chrome with the current language', async () => {
    render(MobileSettingsView);

    expect(screen.getByTestId('mobile-header-title')).toHaveTextContent('설정');
    expect(screen.getByTestId('mobile-settings-language')).toHaveTextContent('언어');
    expect(screen.getByTestId('mobile-settings-language-current')).toHaveTextContent('한국어');
  });

  test('lists every supported locale and persists a language switch', async () => {
    render(MobileSettingsView);

    await fireEvent.click(screen.getByTestId('mobile-settings-language'));

    for (const locale of SUPPORTED_LOCALES) {
      expect(screen.getByRole('option', { name: locale.name })).toBeInTheDocument();
    }

    await fireEvent.click(screen.getByRole('option', { name: 'Русский' }));

    // Wait on the re-rendered chrome: the runtime import is async, so the
    // header text is the observable signal that the switch completed.
    await waitFor(() =>
      expect(screen.getByTestId('mobile-header-title')).toHaveTextContent('Настройки'),
    );

    expect(updateUserRegionalSettings).toHaveBeenCalledWith(42, {
      timezone: 'Europe/Berlin',
      language: 'ru',
    });
    expect(authState.user.language).toBe('ru');
    expect(i18n.locale).toBe('ru');
    expect(screen.getByTestId('mobile-settings-language-current')).toHaveTextContent('Русский');
  });

  test('keeps the previous language and surfaces a toast when the save fails', async () => {
    updateUserRegionalSettings.mockRejectedValue(new Error('network down'));
    render(MobileSettingsView);

    await fireEvent.click(screen.getByTestId('mobile-settings-language'));
    await fireEvent.click(screen.getByRole('option', { name: 'Русский' }));

    await waitFor(() => expect(errorToast).toHaveBeenCalledWith(t('mobile.settings.saveFailed')));
    expect(i18n.locale).toBe('ko');
    expect(authState.user.language).toBe('ko');
    expect(screen.getByTestId('mobile-settings-language-current')).toHaveTextContent('한국어');
  });

  test('does not call the API when the current language is re-selected', async () => {
    render(MobileSettingsView);

    await fireEvent.click(screen.getByTestId('mobile-settings-language'));
    await fireEvent.click(screen.getByRole('option', { name: '한국어' }));

    expect(updateUserRegionalSettings).not.toHaveBeenCalled();
    expect(i18n.locale).toBe('ko');
  });
});
