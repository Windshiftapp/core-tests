import { describe, expect, it } from 'vitest';

import de from './de/index.js';

function collectLeafStrings(object) {
  const values = [];
  for (const value of Object.values(object)) {
    if (value !== null && typeof value === 'object' && !Array.isArray(value)) {
      values.push(...collectLeafStrings(value));
    } else if (typeof value === 'string') {
      values.push(value);
    }
  }
  return values;
}

function resolve(object, path) {
  return path.split('.').reduce((acc, key) => acc?.[key], object);
}

// German names work items "Vorgang" (singular) and "Vorgänge" (plural). This
// pins the user-facing term so a later translation cannot silently reintroduce
// "Arbeitselement".
describe('German work-item terminology', () => {
  it.each([
    ['common.ticket', 'Vorgang'],
    ['common.tickets', 'Vorgänge'],
    ['common.item', 'Vorgang'],
    ['common.items', 'Vorgänge'],
    ['common.noItemsFound', 'Keine Vorgänge gefunden'],
    ['pickers.noItemsFound', 'Keine Vorgänge gefunden'],
  ])('translates %s as %s', (key, expected) => {
    expect(resolve(de, key)).toBe(expected);
  });

  it('does not use "Arbeitselement" anywhere in the German catalog', () => {
    const offenders = collectLeafStrings(de).filter((value) => value.includes('Arbeitselement'));

    expect(offenders).toEqual([]);
  });
});
