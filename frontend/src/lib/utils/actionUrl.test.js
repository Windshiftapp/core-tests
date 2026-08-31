import { describe, expect, it } from 'vitest';
import { itemIdFromActionUrl, mobileActionUrl } from './actionUrl.js';

describe('itemIdFromActionUrl', () => {
  it('extracts the id from a desktop workspace item deep link', () => {
    expect(itemIdFromActionUrl('/workspaces/2/items/481')).toBe('481');
  });

  it('extracts the id from a nested-collection deep link', () => {
    expect(itemIdFromActionUrl('/workspaces/2/collections/9/items/481')).toBe('481');
  });

  it('extracts the id when a query string trails the item segment', () => {
    expect(itemIdFromActionUrl('/workspaces/2/items/481?scroll=comments')).toBe('481');
  });

  it('returns null for non-item urls', () => {
    expect(itemIdFromActionUrl('/m/something-else')).toBeNull();
  });

  it('returns null for empty/missing input', () => {
    expect(itemIdFromActionUrl('')).toBeNull();
    expect(itemIdFromActionUrl(undefined)).toBeNull();
  });
});

describe('mobileActionUrl', () => {
  it('rewrites an item deep link to the mobile route', () => {
    expect(mobileActionUrl('/workspaces/2/items/481')).toBe('/m/items/481');
  });

  it('rewrites a nested-collection deep link to the mobile route', () => {
    expect(mobileActionUrl('/workspaces/2/collections/9/items/481')).toBe('/m/items/481');
  });

  it('passes a non-item url through unchanged', () => {
    expect(mobileActionUrl('/m/something-else')).toBe('/m/something-else');
  });

  it('returns null for empty/missing input so callers can fall back', () => {
    expect(mobileActionUrl('')).toBeNull();
    expect(mobileActionUrl(undefined)).toBeNull();
  });
});
