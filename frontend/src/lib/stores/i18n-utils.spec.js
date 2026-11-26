// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  buildDefaultDashboardLayout,
  getDashboardSectionDisplay,
  getDashboardSectionSaveValues,
} from '../services/dashboardWidgetRegistry.js';
import { formatRelativeTime } from '../utils/dateFormatter.js';
import { EAGER_STARTUP_COPY, getStartupCopy } from '../utils/startupCopy.js';
import { i18n } from './i18n.svelte.js';
import { getPluralCategory, negotiateLocale } from './i18n-utils.js';

const supportedLocales = [
  { code: 'en' },
  { code: 'de' },
  { code: 'es' },
  { code: 'ar' },
  { code: 'pt-BR' },
  { code: 'ru' },
  { code: 'zh-CN' },
  { code: 'fr' },
];

describe('negotiateLocale', () => {
  it.each([
    ['pt-BR', 'pt-BR'],
    ['pt-br', 'pt-BR'],
    ['pt_BR', 'pt-BR'],
    ['zh-CN', 'zh-CN'],
    ['zh_cn', 'zh-CN'],
    ['de-CH', 'de'],
    ['ru', 'ru'],
    ['ru-RU', 'ru'],
    ['ru_RU', 'ru'],
    ['fr-FR', 'fr'],
  ])('maps %s to %s', (browserLocale, expected) => {
    expect(negotiateLocale(browserLocale, supportedLocales, 'en')).toBe(expected);
  });
});

describe('getPluralCategory', () => {
  it.each([
    [0, 'zero'],
    [1, 'one'],
    [2, 'two'],
    [3, 'few'],
    [11, 'many'],
    [100, 'other'],
  ])('selects the Arabic category for %d', (count, expected) => {
    expect(getPluralCategory('ar', count)).toBe(expected);
  });

  it.each([
    [0, 'many'],
    [1, 'one'],
    [2, 'few'],
    [5, 'many'],
    [11, 'many'],
    [21, 'one'],
    [22, 'few'],
    [25, 'many'],
  ])('selects the Russian category for %d', (count, expected) => {
    expect(getPluralCategory('ru', count)).toBe(expected);
  });

  it('uses the single Simplified Chinese category', () => {
    expect(getPluralCategory('zh-CN', 0)).toBe('other');
    expect(getPluralCategory('zh-CN', 2)).toBe('other');
  });
});

describe('startup copy', () => {
  it('does not touch the translator before i18n is ready', () => {
    const translate = vi.fn((key) => `translated:${key}`);

    for (const [key, value] of Object.entries(EAGER_STARTUP_COPY)) {
      expect(getStartupCopy(key, false, translate)).toBe(value);
    }
    expect(translate).not.toHaveBeenCalled();
  });

  it('uses the active locale after i18n is ready', () => {
    const translate = vi.fn((key) => `translated:${key}`);

    expect(getStartupCopy('common.retry', true, translate)).toBe('translated:common.retry');
    expect(translate).toHaveBeenCalledWith('common.retry');
  });
});

describe('dashboard section localization', () => {
  it('translates untouched default sections', () => {
    const [section] = buildDefaultDashboardLayout().sections;
    const translate = (key) => `translated:${key}`;

    expect(getDashboardSectionDisplay(section, translate)).toEqual({
      title: 'translated:dashboard.sections.yourDay.title',
      subtitle: 'translated:dashboard.sections.yourDay.subtitle',
    });
  });

  it('preserves customized section text', () => {
    const section = {
      ...buildDefaultDashboardLayout().sections[0],
      title: 'My focus',
      subtitle: 'What matters now',
    };

    expect(getDashboardSectionDisplay(section, () => 'translated')).toEqual({
      title: 'My focus',
      subtitle: 'What matters now',
    });
  });

  it('does not persist localized default text when an unchanged section is saved', () => {
    const [section] = buildDefaultDashboardLayout().sections;
    const german = (key) =>
      ({
        'dashboard.sections.yourDay.title': 'Dein Tag',
        'dashboard.sections.yourDay.subtitle':
          'Ein kurzer Blick auf alles, was Ihre Aufmerksamkeit braucht',
      })[key];
    const english = (key) =>
      ({
        'dashboard.sections.yourDay.title': 'Your Day',
        'dashboard.sections.yourDay.subtitle': 'A quick read on what needs your attention',
      })[key];
    const displayedInGerman = getDashboardSectionDisplay(section, german);

    const savedValues = getDashboardSectionSaveValues(section, displayedInGerman, german);
    const savedSection = { ...section, ...savedValues };

    expect(savedValues).toEqual({
      title: 'Your Day',
      subtitle: 'A quick read on what needs your attention',
    });
    expect(getDashboardSectionDisplay(savedSection, english)).toEqual({
      title: 'Your Day',
      subtitle: 'A quick read on what needs your attention',
    });
    expect(getDashboardSectionDisplay(savedSection, german)).toEqual(displayedInGerman);
  });

  it('persists actual edits made to localized section text', () => {
    const [section] = buildDefaultDashboardLayout().sections;
    const translate = (key) => `translated:${key}`;

    expect(
      getDashboardSectionSaveValues(
        section,
        { title: 'Mein Fokus', subtitle: 'Heute wichtig' },
        translate
      )
    ).toEqual({
      title: 'Mein Fokus',
      subtitle: 'Heute wichtig',
    });
  });

  it('preserves edits for custom sections', () => {
    const section = {
      id: 'custom-section',
      title: 'Eigener Bereich',
      subtitle: 'Meine Übersicht',
    };

    expect(
      getDashboardSectionSaveValues(
        section,
        { title: 'Neuer Bereich', subtitle: 'Neue Übersicht' },
        () => 'translated'
      )
    ).toEqual({
      title: 'Neuer Bereich',
      subtitle: 'Neue Übersicht',
    });
  });
});

describe('locale-aware relative time', () => {
  afterEach(async () => {
    vi.useRealTimers();
    await i18n.setLocale('en');
  });

  it('uses the active application locale', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-08-28T12:00:00Z'));
    await i18n.setLocale('de');

    expect(formatRelativeTime('2026-08-28T11:39:00Z')).toBe('vor 21 Minuten');
  });

  it('keeps the English fallback behavior', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-08-28T12:00:00Z'));
    await i18n.setLocale('en');

    expect(formatRelativeTime('2026-08-28T11:39:00Z')).toBe('21 minutes ago');
  });

  it('describes elapsed weeks and months instead of calendar periods', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-08-28T12:00:00Z'));
    await i18n.setLocale('en');

    expect(formatRelativeTime('2026-08-20T12:00:00Z')).toBe('1 week ago');
    expect(formatRelativeTime('2026-07-14T12:00:00Z')).toBe('1 month ago');
    expect(formatRelativeTime('2026-08-27T02:00:00Z')).toBe('1 day ago');
    expect(formatRelativeTime('2026-09-05T12:00:00Z')).toBe('in 1 week');
  });
});

describe('Korean locale', () => {
  afterEach(async () => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    await i18n.setLocale('en');
    localStorage.removeItem('windshift-locale');
  });

  it('loads Korean for a ko-KR browser and persists the selection', async () => {
    localStorage.removeItem('windshift-locale');
    vi.spyOn(navigator, 'language', 'get').mockReturnValue('ko-KR');

    await i18n.init();

    expect(i18n.locale).toBe('ko');
    expect(i18n.t('common.save')).toBe('저장');
    expect(i18n.t('auth.login')).toBe('로그인');
    expect(localStorage.getItem('windshift-locale')).toBe('ko');
    expect(document.documentElement.lang).toBe('ko');
    expect(document.documentElement.dir).toBe('ltr');
  });

  it('restores saved Korean even when the browser uses English', async () => {
    localStorage.setItem('windshift-locale', 'ko');
    vi.spyOn(navigator, 'language', 'get').mockReturnValue('en-US');

    await i18n.init();

    expect(i18n.locale).toBe('ko');
    expect(i18n.t('common.cancel')).toBe('취소');
  });

  it.each([0, 1, 2])('formats %d items without an English plural suffix', async (count) => {
    await i18n.setLocale('ko');
    const plural = count === 1 ? '' : 's';

    expect(i18n.t('statuses.statuses', { count })).toBe(`상태 ${count}개`);
    expect(i18n.t('collections.childItems', { count, plural })).toBe(`하위 작업 ${count}개`);
    expect(i18n.t('collections.showingItems', { count, type: '작업', plural })).toBe(
      `작업 ${count}개 표시 중`
    );
    expect(i18n.t('items.storyPointsChildRollup', { count, points: 8, plural })).toBe(
      `하위 작업 ${count}개에서 8포인트 합산`
    );
  });

  it('formats relative dates in Korean', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-08-28T12:00:00Z'));
    await i18n.setLocale('ko');

    expect(formatRelativeTime('2026-08-28T11:39:00Z')).toBe('21분 전');
  });
});
