import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api.js', () => ({
  api: {
    ai: {
      planMyDay: vi.fn(),
    },
    llmProviders: {
      getEnabled: vi.fn(),
    },
  },
  fetchAPI: vi.fn(),
}));

vi.mock('../../router.js', () => ({
  navigate: vi.fn(),
}));

vi.mock('../../stores', () => ({
  authStore: {
    currentUser: null,
  },
}));

import { api } from '../../api.js';
import PlanMyDay from './PlanMyDay.svelte';

describe('PlanMyDay', () => {
  beforeEach(() => {
    api.llmProviders.getEnabled.mockResolvedValue(null);
    api.ai.planMyDay.mockResolvedValue({
      summary: '',
      activities: [],
    });
  });

  it('supports an empty provider list without crashing', async () => {
    render(PlanMyDay);

    await waitFor(() => {
      expect(api.llmProviders.getEnabled).toHaveBeenCalledOnce();
    });
    const generateButton = screen.getByTestId('plan-my-day-generate');
    expect(generateButton).toBeInTheDocument();

    await fireEvent.click(generateButton);

    await waitFor(() => {
      expect(api.ai.planMyDay).toHaveBeenCalledWith(null);
    });
    expect(screen.getByTestId('plan-my-day-view')).toBeInTheDocument();
  });
});
