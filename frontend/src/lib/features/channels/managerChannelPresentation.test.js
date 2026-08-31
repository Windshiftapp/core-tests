import { describe, expect, test } from 'vitest';
import { managerChannelPurpose, managerChannelStatusColor } from './managerChannelPresentation.js';

describe('managerChannelPurpose', () => {
  test.each([
    [
      { type: 'portal', config: '{"portal_slug":"support"}' },
      'channels.manager.publishedAt',
      { path: '/portal/support' },
    ],
    [
      { type: 'form', config: { form_slug: 'feedback' } },
      'channels.manager.publishedAt',
      { path: '/forms/feedback' },
    ],
    [
      { type: 'email', config: '{"email_workspace_id":42}' },
      'channels.manager.deliversTo',
      { workspace: 'Service Desk' },
    ],
    [{ type: 'smtp', config: '{}' }, 'channels.manager.outboundNotifications', {}],
    [{ type: 'webhook', config: '{}' }, 'channels.manager.outboundEvents', {}],
  ])('describes %s in manager language', (channel, key, params) => {
    expect(managerChannelPurpose(channel, 'Service Desk')).toEqual({ key, params });
  });

  test('fails safely for malformed legacy config', () => {
    expect(managerChannelPurpose({ type: 'portal', config: '{' })).toEqual({
      key: 'channels.manager.operational',
      params: {},
    });
  });
});

describe('managerChannelStatusColor', () => {
  test.each(['enabled', 'active', 'configured'])('%s is green', (status) => {
    expect(managerChannelStatusColor(status)).toBe('green');
  });

  test.each(['disabled', 'pending', undefined])('%s is neutral', (status) => {
    expect(managerChannelStatusColor(status)).toBe('gray');
  });
});
