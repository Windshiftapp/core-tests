import { describe, expect, it } from 'vitest';
import { hasParentReference, resolveParentKey } from './parentReference.js';

describe('resolveParentKey', () => {
  it('returns the server permission-checked parent key', () => {
    expect(resolveParentKey({ parent_key: 'EPIC-7' })).toBe('EPIC-7');
  });

  it('returns an empty key when the parent is masked or absent', () => {
    expect(resolveParentKey({ parent_id: 7 })).toBe('');
    expect(resolveParentKey({})).toBe('');
    expect(resolveParentKey(null)).toBe('');
  });
});

describe('hasParentReference', () => {
  it('is false without a parent', () => {
    expect(hasParentReference({ id: 1 })).toBe(false);
  });

  it('is false when a masked parent keeps only the raw id', () => {
    expect(hasParentReference({ parent_id: 7, parent_key: '', parent_title: '' })).toBe(false);
  });

  it('is true when a key or title is available', () => {
    expect(hasParentReference({ parent_id: 7, parent_key: 'EPIC-7' })).toBe(true);
    expect(hasParentReference({ parent_id: 7, parent_title: 'Release' })).toBe(true);
  });
});
