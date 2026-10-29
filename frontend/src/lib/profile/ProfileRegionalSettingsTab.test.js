import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ updateUserRegionalSettings: vi.fn() }));

vi.mock('../api/users.js', () => ({
  updateUserRegionalSettings: mocks.updateUserRegionalSettings,
}));

vi.mock('../stores/auth.svelte.js', () => ({
  authStore: { patchCurrentUser: vi.fn() },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  i18n: { locale: 'en', setLocale: vi.fn() },
  SUPPORTED_LOCALES: [{ code: 'en', name: 'English' }],
  t: (key) => key,
}));

beforeAll(() => {
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {};
  }
  if (!globalThis.ResizeObserver) {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  }
});

import ProfileRegionalSettingsTab from './ProfileRegionalSettingsTab.svelte';
import { updateUserRegionalSettings } from '../api/users.js';

afterEach(() => {
  cleanup();
  document.body.innerHTML = '';
});

describe('ProfileRegionalSettingsTab timezone picker', () => {
  it('offers South American zones such as America/Sao_Paulo', async () => {
    render(ProfileRegionalSettingsTab, {
      props: { user: { id: 7, timezone: 'UTC', language: 'en' }, userId: 7 },
    });

    const input = screen.getByPlaceholderText('users.timezone');
    await fireEvent.click(input);
    await fireEvent.input(input, { target: { value: 'Sao' } });

    await waitFor(() => {
      expect(document.querySelector('[data-option-value="America/Sao_Paulo"]')).toBeInTheDocument();
    });
  });
});
