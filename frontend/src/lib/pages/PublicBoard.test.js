import { render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api/publicBoard.js', () => ({
  publicBoard: {
    get: vi.fn(),
  },
}));

vi.mock('../stores/theme.svelte.js', () => ({
  themeStore: {
    resolvedTheme: 'light',
    init: vi.fn(),
    setColorMode: vi.fn(),
  },
}));

const { publicBoard } = await import('../api/publicBoard.js');
const { default: PublicBoard } = await import('./PublicBoard.svelte');

describe('PublicBoard response contracts', () => {
  beforeEach(() => vi.clearAllMocks());

  it('renders supported status fields and warns when column counts are partial', async () => {
    publicBoard.get.mockResolvedValue({
      collection: { name: 'Roadmap', description: '' },
      card_fields: [{ field_identifier: 'status', field_type: 'system' }],
      total_items: 501,
      loaded_items: 500,
      truncated: true,
      item_limit: 500,
      columns: [
        {
          name: 'Open',
          color: '#64748b',
          items: [{ key: 'ROAD-1', title: 'Ship', status_name: 'Open' }],
        },
      ],
    });

    render(PublicBoard, { props: { slug: 'roadmap' } });

    const warning = await screen.findByTestId('public-board-truncated');
    expect(warning.textContent).toContain('newest 500 of 501');
    expect(warning.textContent).toContain('Column counts are partial');
    expect(screen.getByTestId('public-board-card-status').textContent).toContain('Open');
    expect(screen.queryByTestId('public-board-card-title')).toBeNull();
  });

  it('maps every approved card-field configuration to its public response value', async () => {
    publicBoard.get.mockResolvedValue({
      collection: { name: 'Roadmap', description: '' },
      card_fields: [
        'key',
        'title',
        'status',
        'priority',
        'assignee',
        'item_type',
        'story_points',
        'due_date',
        'labels',
      ].map((field_identifier, display_order) => ({
        field_identifier,
        field_type: 'system',
        display_order,
      })),
      total_items: 1,
      loaded_items: 1,
      truncated: false,
      item_limit: 500,
      columns: [
        {
          name: 'Open',
          color: '#64748b',
          items: [
            {
              key: 'ROAD-42',
              title: 'Ship the integration',
              status_name: 'In progress',
              priority_name: 'High',
              priority_color: '#dc2626',
              assignee_name: 'Ada Lovelace',
              item_type_name: 'Story',
              story_points: 8,
              due_date: '2026-08-01',
              labels: [{ name: 'Release', color: '#2563eb' }],
            },
          ],
        },
      ],
    });

    render(PublicBoard, { props: { slug: 'roadmap' } });

    const card = await screen.findByTestId('public-board-card');
    for (const expected of [
      'ROAD-42',
      'Ship the integration',
      'In progress',
      'High',
      'Story',
      '8 SP',
      '2026-08-01',
      'Release',
    ]) {
      expect(card.textContent).toContain(expected);
    }
    expect(screen.getByTitle('Ada Lovelace')).toBeTruthy();
  });
});
