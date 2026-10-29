import { describe, expect, test } from 'vitest';
import { defaultCustomFieldValue } from './customFieldTypes.js';

describe('defaultCustomFieldValue', () => {
  test('array-valued types start as an empty array', () => {
    expect(defaultCustomFieldValue('multiselect')).toEqual([]);
    expect(defaultCustomFieldValue('multi_user')).toEqual([]);
    expect(defaultCustomFieldValue('multi_milestone')).toEqual([]);
  });

  test('scalar types start empty', () => {
    expect(defaultCustomFieldValue('milestone')).toBe('');
    expect(defaultCustomFieldValue('text')).toBe('');
  });
});
