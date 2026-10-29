import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api/milestones.js', () => ({
  milestones: { getAll: vi.fn(async () => []) },
  milestoneCategories: { getAll: vi.fn().mockResolvedValue([]) },
}));

import TestManagementHeaderHarness from './TestManagementHeaderHarness.svelte';
import { milestones } from '../../api/milestones.js';

beforeEach(() => {
  window.history.replaceState({}, '', '/workspaces/7/tests/sets?milestone=42');
});

afterEach(cleanup);

describe('TestManagementHeader', () => {
  it('hydrates the milestone filter and owns both creation entry points', async () => {
    render(TestManagementHeaderHarness);

    await waitFor(() =>
      expect(screen.getByTestId('header-state')).toHaveAttribute('data-milestone', '42')
    );

    await fireEvent.keyDown(document, { key: 'a' });
    expect(screen.getByTestId('header-state')).toHaveAttribute('data-create-count', '1');

    window.dispatchEvent(new CustomEvent('trigger-test-plan-form'));
    await waitFor(() =>
      expect(screen.getByTestId('header-state')).toHaveAttribute('data-create-count', '2')
    );

    const input = document.createElement('input');
    document.body.append(input);
    await fireEvent.keyDown(input, { key: 'a' });
    expect(screen.getByTestId('header-state')).toHaveAttribute('data-create-count', '2');
    input.remove();
  });
});
