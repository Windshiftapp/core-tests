import { render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';

vi.mock('../../api/incidents.js', () => ({
  itemIncidents: {
        get: vi.fn(),
        trigger: vi.fn(),
        acknowledge: vi.fn(),
        unacknowledge: vi.fn(),
        resolve: vi.fn(),
      },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('../../stores/toasts.svelte.js', () => ({
  errorToast: vi.fn(),
  successToast: vi.fn(),
}));

import { itemIncidents } from '../../api/incidents.js';
import ItemIncidentPanel from './ItemIncidentPanel.svelte';

const triggeredIncident = {
  id: 7,
  status: 'triggered',
  urgency: 'high',
  policy_name: 'Primary',
  triggered_at: '2026-10-01T10:00:00Z',
  escalation_repeat_count: 0,
};

describe('ItemIncidentPanel', () => {
  it('does not request an incident for an item without an incident pointer', async () => {
    render(ItemIncidentPanel, {
      itemId: 42,
      item: { id: 42, team_id: 3 },
      canEdit: true,
    });

    expect(await screen.findByTestId('item-incident-trigger')).toBeInTheDocument();
    expect(itemIncidents.get).not.toHaveBeenCalled();
    expect(screen.queryByTestId('item-incident-loading')).not.toBeInTheDocument();
  });

  it('loads and renders the incident when the item points at one', async () => {
    itemIncidents.get.mockResolvedValue(triggeredIncident);

    render(ItemIncidentPanel, {
      itemId: 42,
      item: { id: 42, team_id: 3, incident_id: 7 },
      canEdit: true,
    });

    const panel = await screen.findByTestId('item-incident-panel');
    expect(itemIncidents.get).toHaveBeenCalledWith(42);
    expect(panel).toHaveAttribute('data-incident-status', 'triggered');
    expect(screen.getByTestId('item-incident-status')).toHaveTextContent('items.incidentTriggered');
  });

  it('treats a missing incident as empty when the pointer is stale', async () => {
    const notFound = Object.assign(new Error('not found'), { status: 404 });
    itemIncidents.get.mockRejectedValue(notFound);

    render(ItemIncidentPanel, {
      itemId: 42,
      item: { id: 42, team_id: 3, incident_id: 7 },
      canEdit: true,
    });

    expect(await screen.findByTestId('item-incident-trigger')).toBeInTheDocument();
    expect(itemIncidents.get).toHaveBeenCalledWith(42);
    expect(screen.queryByTestId('item-incident-panel')).not.toBeInTheDocument();
  });
});
