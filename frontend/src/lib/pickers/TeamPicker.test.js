import { fireEvent, render, screen } from '@testing-library/svelte';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api/teams.js', () => ({
  teams: { getAll: vi.fn() },
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

import { teams } from '../api/teams.js';
import TeamPicker from './TeamPicker.svelte';

beforeAll(() => {
  if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {};
  if (!globalThis.ResizeObserver) {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  }
});

beforeEach(() => {
  vi.clearAllMocks();
  teams.getAll.mockResolvedValue([]);
});

describe('TeamPicker provided options', () => {
  it('does not fetch on mount when teams are provided', async () => {
    render(TeamPicker, { props: { teams: [], value: null } });

    await Promise.resolve();
    expect(teams.getAll).not.toHaveBeenCalled();
  });

  it('calls onOpen when the picker opens', async () => {
    const onOpen = vi.fn();
    render(TeamPicker, { props: { teams: [], value: null, onOpen } });

    await fireEvent.click(screen.getByTestId('team-picker-trigger'));

    expect(onOpen).toHaveBeenCalledTimes(1);
    expect(teams.getAll).not.toHaveBeenCalled();
  });
});
