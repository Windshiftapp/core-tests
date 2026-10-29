import { describe, expect, test } from 'vitest';
import { delegatedAdminItems, filterDelegatedAdminItems } from './delegatedAdminNavigation.js';

const baseState = {
  isSystemAdmin: false,
  canManageChannels: false,
  canManageAssets: false,
};

describe('delegated admin registry', () => {
  test('every surface lives under /manage', () => {
    for (const item of delegatedAdminItems) {
      expect(item.href.startsWith('/manage/')).toBe(true);
    }
  });

  test('exposes channels to a channel manager', () => {
    const items = filterDelegatedAdminItems({ ...baseState, canManageChannels: true });

    expect(items.map((item) => item.id)).toEqual(['channels']);
  });

  test('exposes asset sets to an asset manager', () => {
    const items = filterDelegatedAdminItems({ ...baseState, canManageAssets: true });

    expect(items.map((item) => item.id)).toEqual(['assets']);
  });

  test('exposes every delegated surface to a user who holds both', () => {
    const items = filterDelegatedAdminItems({
      ...baseState,
      canManageChannels: true,
      canManageAssets: true,
    });

    expect(items.map((item) => item.id)).toEqual(['channels', 'assets']);
  });

  test('hides the delegated surface from a user with no capability', () => {
    expect(filterDelegatedAdminItems(baseState)).toEqual([]);
  });

  test('hides the delegated surface from a system admin', () => {
    expect(
      filterDelegatedAdminItems({
        isSystemAdmin: true,
        canManageChannels: true,
        canManageAssets: true,
      })
    ).toEqual([]);
  });

  test('tolerates an unhydrated permission state', () => {
    expect(filterDelegatedAdminItems(undefined)).toEqual([]);
  });
});
