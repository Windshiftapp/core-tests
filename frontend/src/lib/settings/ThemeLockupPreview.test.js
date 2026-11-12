import { render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';

// The component only needs the translation function; mocking i18n keeps the
// locale catalogs out of this focused render test.
vi.mock('../stores/i18n.svelte.js', () => ({
  t: (_key, fallback) => fallback ?? _key,
}));

import ThemeLockupPreview from './ThemeLockupPreview.svelte';

const baseProps = {
  siteTitle: 'Acme',
  brandLayout: 'mark_text',
  logoUrl: '/light.png',
  logoUrlDark: '/dark.png',
  logoFullUrl: '/full-light.png',
  logoFullUrlDark: '/full-dark.png',
  lightBackground: '#ffffff',
  lightText: '#111827',
  darkBackground: '#1f2937',
  darkText: '#f3f4f6',
};

function preview(testid) {
  return screen.getByTestId(`theme-brand-preview-${testid}`);
}

function imageSrc(testid) {
  return preview(testid).querySelector('img')?.getAttribute('src');
}

describe('ThemeLockupPreview', () => {
  it('shows the mark plus the site title for mark_text in the expanded preview', () => {
    render(ThemeLockupPreview, { props: baseProps });

    expect(imageSrc('light-expanded')).toBe('/light.png');
    expect(preview('light-expanded')).toHaveTextContent('Acme');
  });

  it('shows only the mark in the collapsed preview for mark_text', () => {
    render(ThemeLockupPreview, { props: baseProps });

    expect(imageSrc('light-collapsed')).toBe('/light.png');
    expect(preview('light-collapsed')).not.toHaveTextContent('Acme');
  });

  it('shows the full logo without text for full_logo in the expanded preview', () => {
    render(ThemeLockupPreview, { props: { ...baseProps, brandLayout: 'full_logo' } });

    expect(imageSrc('light-expanded')).toBe('/full-light.png');
    expect(preview('light-expanded')).not.toHaveTextContent('Acme');
  });

  it('uses the dark full logo for the dark expanded preview', () => {
    render(ThemeLockupPreview, { props: { ...baseProps, brandLayout: 'full_logo' } });

    expect(imageSrc('dark-expanded')).toBe('/full-dark.png');
  });

  it('keeps the mark in the collapsed preview even when full_logo is selected', () => {
    render(ThemeLockupPreview, { props: { ...baseProps, brandLayout: 'full_logo' } });

    expect(imageSrc('light-collapsed')).toBe('/light.png');
  });

  it('hides the site title for mark_only', () => {
    render(ThemeLockupPreview, { props: { ...baseProps, brandLayout: 'mark_only' } });

    expect(imageSrc('light-expanded')).toBe('/light.png');
    expect(preview('light-expanded')).not.toHaveTextContent('Acme');
  });

  it('falls back to the mark when full_logo is selected without a full logo', () => {
    render(ThemeLockupPreview, {
      props: { ...baseProps, brandLayout: 'full_logo', logoFullUrl: '', logoFullUrlDark: '' },
    });

    expect(imageSrc('light-expanded')).toBe('/light.png');
  });

  it('falls back to the built-in mark when no logo is set', () => {
    render(ThemeLockupPreview, {
      props: {
        ...baseProps,
        logoUrl: '',
        logoUrlDark: '',
        logoFullUrl: '',
        logoFullUrlDark: '',
      },
    });

    expect(imageSrc('light-expanded')).toBe('windshift-3.svg');
  });
});
