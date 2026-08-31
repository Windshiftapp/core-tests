import { describe, expect, test } from 'vitest';
import { channelAdminRoute } from './channelRoutes.js';

describe('channelAdminRoute', () => {
  test('opens form channels in the dedicated form builder', () => {
    expect(channelAdminRoute({ id: 17, type: 'form' })).toBe('/admin/channels/17/forms');
  });

  test('opens portal channels in the dedicated portal workspace', () => {
    expect(channelAdminRoute({ id: 18, type: 'portal' })).toBe('/admin/channels/18/portal');
  });

  test('keeps other channel types in the configuration modal', () => {
    expect(channelAdminRoute({ id: 19, type: 'email' })).toBeNull();
  });
});
