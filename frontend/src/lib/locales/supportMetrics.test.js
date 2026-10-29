import { describe, expect, it } from 'vitest';

import ar from './ar/index.js';
import de from './de/index.js';
import en from './en/index.js';
import es from './es/index.js';
import fr from './fr/index.js';
import ko from './ko/index.js';
import ptBR from './pt-BR/index.js';
import ru from './ru/index.js';
import zhCN from './zh-CN/index.js';

// Keys shipped with the support metrics widgets (WI-1133). The runtime falls
// back to English for untranslated keys, but a newly shipped helpdesk surface
// must be translated in every locale rather than silently showing English.
const SUPPORT_METRIC_KEYS = [
  'dashboard.widgetCatalog.supportOverview.name',
  'dashboard.widgetCatalog.supportOverview.description',
  'dashboard.customization.support.name',
  'dashboard.customization.support.description',
  'dashboard.states.supportMetricsLoadError',
  'dashboard.states.supportMetricsEmpty',
  'dashboard.states.supportOverviewSetupTitle',
  'dashboard.states.supportOverviewSetupSubtitle',
  'dashboard.states.supportSourcesUnavailable',
  'dashboard.states.supportBacklog',
  'dashboard.states.supportUnassigned',
  'dashboard.states.supportCreated',
  'dashboard.states.supportResolved',
  'dashboard.states.supportSlaCompliance',
  'dashboard.states.supportSlaNone',
  'dashboard.states.supportFirstResponseP50',
  'dashboard.states.supportExportCSV',
  'dashboard.states.supportLiveDelayed',
  'workspaceDashboard.widgets.supportMetrics.name',
  'workspaceDashboard.widgets.supportMetrics.description',
];

const NON_ENGLISH_LOCALES = {
  ar,
  de,
  es,
  fr,
  ko,
  'pt-BR': ptBR,
  ru,
  'zh-CN': zhCN,
};

function resolve(object, path) {
  return path.split('.').reduce((acc, key) => acc?.[key], object);
}

function placeholdersOf(text) {
  return [...String(text).matchAll(/\{[^}]+\}/g)].map((match) => match[0]).sort();
}

describe('support metrics translations', () => {
  it('defines every support metrics key in the English catalog', () => {
    const missing = SUPPORT_METRIC_KEYS.filter((key) => typeof resolve(en, key) !== 'string');

    expect(missing).toEqual([]);
  });

  it.each(Object.entries(NON_ENGLISH_LOCALES))(
    '%s translates every support metrics key',
    (_code, catalog) => {
      const missing = SUPPORT_METRIC_KEYS.filter((key) => {
        const value = resolve(catalog, key);
        return typeof value !== 'string' || value.trim() === '';
      });

      expect(missing).toEqual([]);
    }
  );

  it.each(Object.entries(NON_ENGLISH_LOCALES))(
    '%s keeps the support metrics placeholders',
    (_code, catalog) => {
      const mismatches = SUPPORT_METRIC_KEYS.map((key) => ({
        key,
        english: placeholdersOf(resolve(en, key)),
        localized: placeholdersOf(resolve(catalog, key)),
      })).filter(({ english, localized }) => english.join() !== localized.join());

      expect(mismatches).toEqual([]);
    }
  );
});
