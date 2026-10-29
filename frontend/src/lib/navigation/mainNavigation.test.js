import { IconWorldSearch } from '@tabler/icons-svelte-runes';
import { describe, expect, test } from 'vitest';
import { mainNavItems } from './mainNavigation.js';

describe('channel manager navigation', () => {
  test('is not part of the global navigation because it moved to delegated admin', () => {
    expect(mainNavItems.find((item) => item.id === 'channel-management')).toBeUndefined();
  });
});

describe('portal hub navigation', () => {
  test('uses the portal hub icon', () => {
    const portalHub = mainNavItems.find((item) => item.id === 'portal-hub');

    expect(portalHub?.icon).toBe(IconWorldSearch);
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
