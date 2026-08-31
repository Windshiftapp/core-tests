import { IconLifebuoy, IconMessage2Plus } from '@tabler/icons-svelte-runes';
import { describe, expect, test } from 'vitest';
import { mainNavItems } from './mainNavigation.js';

describe('channel manager navigation', () => {
  test('is permission-gated and routes to the non-admin surface', () => {
    const channels = mainNavItems.find((item) => item.id === 'channel-management');

    expect(channels).toMatchObject({
      labelKey: 'nav.channels',
      href: '/manage/channels',
      permission: 'canManageChannels',
      activeViews: ['channel-manager'],
    });
  });
});

describe('portal hub navigation', () => {
  test('uses an icon distinct from channel management', () => {
    const channelManagement = mainNavItems.find((item) => item.id === 'channel-management');
    const portalHub = mainNavItems.find((item) => item.id === 'portal-hub');

    expect(channelManagement?.icon).toBe(IconLifebuoy);
    expect(portalHub?.icon).toBe(IconMessage2Plus);
    expect(portalHub?.icon).not.toBe(channelManagement?.icon);
  });
});

describe('team navigation', () => {
  test('exposes the supported Teams surface', () => {
    expect(mainNavItems.find((item) => item.id === 'teams')).toMatchObject({
      labelKey: 'nav.teams',
      href: '/teams',
      activeViews: ['teams-list', 'team-detail'],
    });
  });
});
