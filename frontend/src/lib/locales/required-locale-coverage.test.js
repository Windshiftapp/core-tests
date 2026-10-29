import { describe, expect, it } from 'vitest';

import de from './de/index.js';
import en from './en/index.js';
import fr from './fr/index.js';
import zhCN from './zh-CN/index.js';

const GUARDED_LOCALES = { de, fr, 'zh-CN': zhCN };

function collectLeafKeys(object, prefix = '') {
  const keys = [];
  for (const [key, value] of Object.entries(object)) {
    const path = prefix ? `${prefix}.${key}` : key;
    if (value !== null && typeof value === 'object' && !Array.isArray(value)) {
      keys.push(...collectLeafKeys(value, path));
    } else {
      keys.push(path);
    }
  }
  return keys;
}

function resolve(object, path) {
  return path.split('.').reduce((acc, part) => acc?.[part], object);
}

// The `{plural}` token is an internal selector, not a user-facing placeholder,
// so the guard ignores it when comparing placeholder sets.
function placeholdersOf(text) {
  return [...String(text).matchAll(/\{(\w+)\}/g)]
    .map((match) => match[1])
    .filter((name) => name !== 'plural')
    .sort();
}

const englishLeafKeys = collectLeafKeys(en);

describe('guarded locale coverage (WI-1659)', () => {
  it('English exposes leaf keys to compare against', () => {
    expect(englishLeafKeys.length).toBeGreaterThan(0);
  });

  for (const [locale, messages] of Object.entries(GUARDED_LOCALES)) {
    it(`${locale} matches the English key set exactly`, () => {
      const englishKeys = new Set(englishLeafKeys);
      const localeKeys = new Set(collectLeafKeys(messages));

      const missing = englishLeafKeys.filter((key) => !localeKeys.has(key)).sort();
      const extra = [...localeKeys].filter((key) => !englishKeys.has(key)).sort();

      expect({ missing, extra }).toEqual({ missing: [], extra: [] });
    });

    it(`${locale} keeps every placeholder from English`, () => {
      const mismatches = [];
      for (const key of englishLeafKeys) {
        const enPlaceholders = placeholdersOf(resolve(en, key));
        const localePlaceholders = placeholdersOf(resolve(messages, key));
        if (enPlaceholders.join(',') !== localePlaceholders.join(',')) {
          mismatches.push({ key, enPlaceholders, localePlaceholders });
        }
      }

      expect(mismatches).toEqual([]);
    });
  }
});
