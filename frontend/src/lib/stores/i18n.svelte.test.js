import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { i18n, SUPPORTED_LOCALES, t } from './i18n.svelte.js';

const PERSIAN_RANGE = /[\u0600-\u06FF]/;

describe('Persian locale registration', () => {
  beforeEach(async () => {
    await i18n.setLocale('en');
  });

  afterEach(() => {
    document.documentElement.dir = '';
    document.documentElement.lang = '';
    localStorage.clear();
  });

  it('is listed as a supported right-to-left locale', () => {
    expect(SUPPORTED_LOCALES.find((locale) => locale.code === 'fa')).toEqual({
      code: 'fa',
      name: 'فارسی',
      direction: 'rtl',
    });
  });

  it('loads the Persian catalog and applies right-to-left document direction', async () => {
    await i18n.setLocale('fa');

    expect(i18n.locale).toBe('fa');
    expect(i18n.direction).toBe('rtl');
    expect(i18n.isRTL).toBe(true);
    expect(document.documentElement.dir).toBe('rtl');
    expect(document.documentElement.lang).toBe('fa');

    const save = t('common.save');
    expect(save).not.toBe('Save');
    expect(save).toMatch(PERSIAN_RANGE);
  });
});

describe('Polish locale registration', () => {
  beforeEach(async () => {
    await i18n.setLocale('en');
  });

  afterEach(() => {
    document.documentElement.dir = '';
    document.documentElement.lang = '';
    localStorage.clear();
  });

  it('is listed as a supported left-to-right locale', () => {
    expect(SUPPORTED_LOCALES.find((locale) => locale.code === 'pl')).toEqual({
      code: 'pl',
      name: 'Polski',
      direction: 'ltr',
    });
  });

  it('loads the Polish catalog and applies left-to-right document direction', async () => {
    await i18n.setLocale('pl');

    expect(i18n.locale).toBe('pl');
    expect(i18n.direction).toBe('ltr');
    expect(i18n.isRTL).toBe(false);
    expect(document.documentElement.dir).toBe('ltr');
    expect(document.documentElement.lang).toBe('pl');

    expect(t('common.save')).toBe('Zapisz');
    expect(t('common.loading')).toContain('Ł');
    expect(t('workspaces.workspaces', { count: 3 })).toBe('3 przestrzenie robocze');
  });
});
