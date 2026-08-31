import { describe, expect, test, vi } from 'vitest';

vi.mock('../stores/auth.svelte.js', () => ({
  authStore: { currentUser: { timezone: 'America/Los_Angeles' } },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  i18n: { locale: 'en-US' },
  t: (key) => key,
}));

import { formatAuthenticatedDateTime } from './authenticatedDateFormatter.js';

describe('authenticated instant formatting', () => {
  test('uses the acting user timezone instead of UTC or the browser timezone', () => {
    expect(formatAuthenticatedDateTime('2026-01-15T00:30:00Z')).toMatch(/Jan 14, 2026.*4:30/);
  });
});
