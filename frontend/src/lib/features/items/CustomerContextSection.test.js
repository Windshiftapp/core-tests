import { fireEvent, render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const getTickets = vi.fn();
const getAssets = vi.fn();

vi.mock('../../api/portal.js', () => ({
  customerOrganisations: {
        getTickets: (id, options) => getTickets(id, options),
      },
}))

vi.mock('../../api/assets.js', () => ({
  itemLinkedAssets: {
        get: (id, options) => getAssets(id, options),
      },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

import CustomerContextSection from './CustomerContextSection.svelte';
import { customerOrganisations } from '../../api/portal.js';
import { itemLinkedAssets } from '../../api/assets.js';


const externalItem = {
  id: 9,
  creator_portal_customer_id: 5,
  creator_portal_customer_name: 'Jane Doe',
  creator_portal_customer_email: 'jane@acme.example',
  creator_customer_organisation_id: 3,
  creator_customer_organisation_name: 'Acme Inc',
};

describe('CustomerContextSection (WI-1139)', () => {
  beforeEach(() => {
    getTickets.mockReset();
    getAssets.mockReset();
  });

  it('stays collapsed and makes no requests until expanded', () => {
    render(CustomerContextSection, { item: externalItem });

    const toggle = screen.getByTestId('customer-context-toggle');
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByTestId('customer-context')).not.toBeInTheDocument();
    expect(getTickets).not.toHaveBeenCalled();
    expect(getAssets).not.toHaveBeenCalled();
  });

  it('loads and renders requester, organisation, organisation tickets, and assets on expand', async () => {
    getTickets.mockResolvedValue([
      {
        id: 42,
        workspace_id: 7,
        workspace_key: 'SUP',
        workspace_item_number: 12,
        title: 'Printer down',
        status_name: 'Open',
        creator_contact_name: 'Bob',
        creator_contact_email: 'bob@acme.example',
      },
    ]);
    getAssets.mockResolvedValue([
      { id: 100, link_id: 55, title: 'ThinkPad X1', type_name: 'Laptop', set_name: 'IT Assets' },
    ]);

    render(CustomerContextSection, { item: externalItem });
    await fireEvent.click(screen.getByTestId('customer-context-toggle'));

    expect(await screen.findByTestId('customer-context')).toBeInTheDocument();
    expect(screen.getByTestId('customer-context-requester')).toHaveTextContent('Jane Doe');
    expect(screen.getByTestId('customer-context-requester')).toHaveTextContent('jane@acme.example');
    expect(screen.getByTestId('customer-context-organisation')).toHaveTextContent('Acme Inc');

    const orgTicket = screen.getByTestId('customer-context-org-ticket-42');
    expect(orgTicket).toHaveAttribute('href', '/workspaces/7/items/42');
    expect(orgTicket).toHaveTextContent('SUP-12');
    expect(orgTicket).toHaveTextContent('Printer down');
    expect(screen.getByTestId('customer-context-asset-100')).toHaveTextContent('ThinkPad X1');

    expect(getTickets).toHaveBeenCalledWith(
      3,
      expect.objectContaining({ signal: expect.anything() })
    );
    expect(getAssets).toHaveBeenCalledWith(
      9,
      expect.objectContaining({ signal: expect.anything() })
    );
  });

  it('does not request organisation tickets when the requester has no organisation', async () => {
    getAssets.mockResolvedValue([]);

    render(CustomerContextSection, {
      item: {
        ...externalItem,
        creator_customer_organisation_id: null,
        creator_customer_organisation_name: '',
      },
    });
    await fireEvent.click(screen.getByTestId('customer-context-toggle'));

    expect(await screen.findByTestId('customer-context')).toBeInTheDocument();
    expect(getTickets).not.toHaveBeenCalled();
    expect(screen.queryByTestId('customer-context-org-tickets')).not.toBeInTheDocument();
    expect(getAssets).toHaveBeenCalledWith(
      9,
      expect.objectContaining({ signal: expect.anything() })
    );
  });

  it('still shows linked assets when organisation tickets are denied', async () => {
    getTickets.mockRejectedValue(Object.assign(new Error('forbidden'), { status: 403 }));
    getAssets.mockResolvedValue([
      { id: 100, link_id: 55, title: 'ThinkPad X1', type_name: 'Laptop', set_name: 'IT Assets' },
    ]);

    render(CustomerContextSection, { item: externalItem });
    await fireEvent.click(screen.getByTestId('customer-context-toggle'));

    expect(await screen.findByTestId('customer-context')).toBeInTheDocument();
    expect(screen.getByTestId('customer-context-org-tickets')).toHaveTextContent(
      'items.customerContextOrgTicketsEmpty'
    );
    expect(screen.getByTestId('customer-context-asset-100')).toHaveTextContent('ThinkPad X1');
  });
});
