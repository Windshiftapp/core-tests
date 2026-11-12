import { describe, expect, it } from 'vitest';
import { hasParentReference, resolveParentKey } from './parentReference.js';

describe('resolveParentKey', () => {
  it('prefers the server permission-checked parent key', () => {
    expect(
      resolveParentKey({
        parent_key: 'EPIC-7',
        parent_workspace_item_number: 3,
        workspace_key: 'CHILD',
      })
    ).toBe('EPIC-7');
  });

  it('derives the key from the child workspace for the older hydrated shape', () => {
    expect(resolveParentKey({ parent_workspace_item_number: 7, workspace_key: 'CHILD' })).toBe(
      'CHILD-7'
    );
  });

  it('accepts an explicit workspace key when the item omits one', () => {
    expect(resolveParentKey({ parent_workspace_item_number: 7 }, 'WS')).toBe('WS-7');
  });

  it('returns an empty key when the parent is masked', () => {
    expect(
      resolveParentKey({
        parent_id: 7,
        parent_workspace_item_number: null,
        workspace_key: 'CHILD',
      })
    ).toBe('');
  });
});

describe('hasParentReference', () => {
  it('is false without a parent', () => {
    expect(hasParentReference({ id: 1 })).toBe(false);
  });

  it('is false when a masked parent keeps only the raw id', () => {
    expect(
      hasParentReference({
        parent_id: 7,
        parent_key: '',
        parent_title: '',
        parent_workspace_item_number: null,
        workspace_key: 'CHILD',
      })
    ).toBe(false);
  });

  it('is true when a key or title is available', () => {
    expect(hasParentReference({ parent_id: 7, parent_key: 'EPIC-7' })).toBe(true);
    expect(hasParentReference({ parent_id: 7, parent_title: 'Release' })).toBe(true);
  });
});
