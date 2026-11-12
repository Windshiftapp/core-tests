import { describe, expect, test, vi } from 'vitest';

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key, params) => (params ? `${key}:${JSON.stringify(params)}` : key),
}));

import { useProgressDetail } from './useProgressDetail.svelte.js';

const STATUS_OPTIONS = [
  { value: 'planned', label: 'Planned', lozengeColor: 'grey' },
  { value: 'active', label: 'Active', lozengeColor: 'blue' },
];

function setup(load) {
  return useProgressDetail({
    load,
    errorLabel: 'iteration progress',
    getStatusOptions: () => STATUS_OPTIONS,
  });
}

describe('useProgressDetail', () => {
  test('loads progress and the entity and expands every category', async () => {
    const load = vi.fn(async () => ({
      progress: {
        status_breakdown: [{ category_name: 'To Do' }, { category_name: 'Done' }],
      },
      entity: { id: 1, name: 'Sprint 1' },
    }));
    const detail = setup(load);

    await detail.loadProgress();

    expect(load).toHaveBeenCalledTimes(1);
    expect(detail.loading).toBe(false);
    expect(detail.error).toBeNull();
    expect(detail.entity).toEqual({ id: 1, name: 'Sprint 1' });
    expect(detail.expandedCategories).toEqual({ 'To Do': true, Done: true });
  });

  test('exposes the load error and clears the loading flag', async () => {
    const detail = setup(async () => {
      throw new Error('boom');
    });

    await detail.loadProgress();

    expect(detail.loading).toBe(false);
    expect(detail.error).toBe('boom');
    expect(detail.progress).toBeNull();
    expect(detail.entity).toBeNull();
  });

  test('falls back to a generic message when the error has no message', async () => {
    const detail = setup(async () => {
      throw {};
    });

    await detail.loadProgress();

    expect(detail.error).toBe(
      'dialogs.alerts.failedToLoad:{"error":"iteration progress"}'
    );
  });

  test('toggles a category and leaves the others alone', async () => {
    const detail = setup(async () => ({
      progress: { status_breakdown: [{ category_name: 'To Do' }] },
      entity: {},
    }));
    await detail.loadProgress();

    detail.toggleCategory('To Do');

    expect(detail.expandedCategories).toEqual({ 'To Do': false });
  });

  test('resolves a status option and falls back to the first one', () => {
    const detail = setup(async () => ({ progress: {}, entity: {} }));

    expect(detail.getStatusInfo('active')).toEqual(STATUS_OPTIONS[1]);
    expect(detail.getStatusInfo('unknown')).toEqual(STATUS_OPTIONS[0]);
  });
});
