import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

;
vi.mock('../../api/agentRuns.js', () => ({ agentRuns: { usage: vi.fn() } }));
vi.mock('../../stores/auth.svelte.js', () => ({
  authStore: { currentUser: { id: 1 } },
}));
vi.mock('../../stores/i18n.svelte.js', () => ({ t: (key) => key, i18n: { locale: 'en' } }));
vi.mock('./activityAttributionData.js', () => ({
  loadAttributedItemHistory: vi.fn(),
  agentOwnerName: () => '',
  historyTelemetryRunIDs: () => [],
  isAIChatAttributed: () => false
}));

import { loadAttributedItemHistory } from './activityAttributionData.js';
import ItemHistory from './ItemHistory.svelte';
import { authStore } from '../../api/agentRuns.js';

afterEach(cleanup);

// A merge writes one history row per side: the canonical names the duplicate
// that was folded in, the duplicate names the canonical. Both carry the other
// ticket's key in resolved_new_value, so the timeline can render a single
// reference line instead of "None -> 2788".
describe('ItemHistory merge entries', () => {
  it('renders a canonical merge entry as the merged duplicate key', async () => {
    loadAttributedItemHistory.mockResolvedValue([
      {
        item_id: 2788,
        changed_at: '2026-10-16T19:26:30Z',
        user_id: 1,
        user_name: 'Stefan Ernst',
        field_name: 'merged_item_id',
        old_value: null,
        new_value: '2763',
        resolved_new_value: 'WI-1763'
      }
    ]);

    render(ItemHistory, { itemId: 2788 });

    const action = await screen.findByText('merged duplicate');
    expect(action).toHaveClass('action');
    expect(screen.getByText('WI-1763')).toHaveClass('new');
    expect(screen.queryByText('None')).toBeNull();
  });

  it('renders a duplicate merge entry as the canonical key', async () => {
    loadAttributedItemHistory.mockResolvedValue([
      {
        item_id: 2763,
        changed_at: '2026-10-16T19:26:30Z',
        user_id: 1,
        user_name: 'Stefan Ernst',
        field_name: 'merged_into_item_id',
        old_value: null,
        new_value: '2788',
        resolved_new_value: 'WI-1782'
      }
    ]);

    render(ItemHistory, { itemId: 2763 });

    expect(await screen.findByText('merged into')).toHaveClass('action');
    expect(screen.getByText('WI-1782')).toHaveClass('new');
    expect(screen.queryByText('None')).toBeNull();
  });
});
