import { describe, expect, it, vi } from 'vitest';

// resolveBrandLockup is pure; mock the API/toast boundaries so importing the
// store does not pull in network or i18n side effects.
vi.mock('../api/misc.js', () => ({ branding: { get: vi.fn() } }));
vi.mock('./toasts.svelte.js', () => ({ surfaceError: vi.fn() }));

import { resolveBrandLockup } from './brand.svelte.js';

const base = {
  siteTitle: 'Acme',
  brandLayout: 'mark_text',
  logoURL: '/light.png',
  logoURLDark: '/dark.png',
  logoFullURL: '/full-light.png',
  logoFullURLDark: '/full-dark.png',
};

describe('resolveBrandLockup', () => {
  it('renders the mark with the site title for mark_text', () => {
    expect(resolveBrandLockup({ isDark: false, expanded: true }, base)).toEqual({
      src: '/light.png',
      variant: 'mark',
      showText: true,
      text: 'Acme',
    });
  });

  it('uses the dark mark in dark mode', () => {
    expect(resolveBrandLockup({ isDark: true, expanded: true }, base).src).toBe('/dark.png');
  });

  it('falls back to the light mark when no dark mark is set', () => {
    expect(
      resolveBrandLockup({ isDark: true, expanded: true }, { ...base, logoURLDark: '' }).src
    ).toBe('/light.png');
  });

  it('renders the mark without text for mark_only', () => {
    expect(
      resolveBrandLockup({ isDark: false, expanded: true }, { ...base, brandLayout: 'mark_only' })
    ).toEqual({ src: '/light.png', variant: 'mark', showText: false, text: 'Acme' });
  });

  it('renders the full logo without text for full_logo', () => {
    expect(
      resolveBrandLockup({ isDark: false, expanded: true }, { ...base, brandLayout: 'full_logo' })
    ).toEqual({ src: '/full-light.png', variant: 'full', showText: false, text: 'Acme' });
  });

  it('uses the dark full logo in dark mode for full_logo', () => {
    expect(
      resolveBrandLockup(
        { isDark: true, expanded: true },
        { ...base, brandLayout: 'full_logo' }
      ).src
    ).toBe('/full-dark.png');
  });

  it('falls back to the mark when full_logo has no full logo', () => {
    expect(
      resolveBrandLockup(
        { isDark: false, expanded: true },
        { ...base, brandLayout: 'full_logo', logoFullURL: '' }
      )
    ).toEqual({ src: '/light.png', variant: 'mark', showText: false, text: 'Acme' });
  });

  it('always uses the mark and hides text when collapsed', () => {
    expect(
      resolveBrandLockup({ isDark: false, expanded: false }, { ...base, brandLayout: 'full_logo' })
    ).toEqual({ src: '/light.png', variant: 'mark', showText: false, text: 'Acme' });
  });

  it('returns no src when no custom logo is configured', () => {
    expect(
      resolveBrandLockup(
        { isDark: false, expanded: true },
        { ...base, logoURL: '', logoURLDark: '', logoFullURL: '', logoFullURLDark: '' }
      ).src
    ).toBeNull();
  });
});
