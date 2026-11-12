import { describe, expect, it } from 'vitest';
import { fieldValue, toIdKeyedCustomFieldValues } from './customFieldValues.js';

const field = { id: 7, name: 'Account tier' };

describe('fieldValue', () => {
  it('prefers the numeric id key over the legacy name mirror', () => {
    expect(fieldValue({ 7: 'gold', 'Account tier': 'stale' }, field)).toBe('gold');
  });

  it('falls back to the legacy name key', () => {
    expect(fieldValue({ 'Account tier': 'gold' }, field)).toBe('gold');
  });

  it('treats an explicit empty id key as cleared instead of falling back', () => {
    expect(fieldValue({ 7: null, 'Account tier': 'stale' }, field)).toBeNull();
    expect(fieldValue({ 7: '', 'Account tier': 'stale' }, field)).toBe('');
  });
});

describe('toIdKeyedCustomFieldValues', () => {
  it('re-keys name values to ids and drops name keys', () => {
    expect(
      toIdKeyedCustomFieldValues({ 'Account tier': 'gold', orphan: 1 }, [field])
    ).toEqual({ 7: 'gold' });
  });

  it('copies an ambiguous name value onto every matching field', () => {
    const duplicates = [
      { id: 8, name: 'Priority' },
      { id: 9, name: 'Priority' },
    ];
    expect(toIdKeyedCustomFieldValues({ Priority: 2 }, duplicates)).toEqual({ 8: 2, 9: 2 });
  });
});
