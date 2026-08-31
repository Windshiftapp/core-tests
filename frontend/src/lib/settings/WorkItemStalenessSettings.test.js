import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api.js', () => ({
  api: {
    workItemStaleness: {
      get: vi.fn(),
      update: vi.fn(),
    },
  },
}));

vi.mock('../stores/toasts.svelte.js', () => ({
  errorToast: vi.fn(),
  successToast: vi.fn(),
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) =>
    ({
      'settings.workItemStaleness.validation': 'Enter a whole number between 1 and 365 days.',
    })[key] ?? key,
}));

vi.mock('../stores/workItemStalenessSettings.svelte.js', () => ({
  workItemStalenessSettings: {
    hydrate: vi.fn(),
  },
}));

import { api } from '../api.js';
import { successToast } from '../stores/toasts.svelte.js';
import { workItemStalenessSettings } from '../stores/workItemStalenessSettings.svelte.js';
import WorkItemStalenessSettings from './WorkItemStalenessSettings.svelte';

describe('WorkItemStalenessSettings', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.workItemStaleness.get.mockResolvedValue({ stale_after_days: 30 });
    api.workItemStaleness.update.mockResolvedValue({ stale_after_days: 45 });
  });

  it('loads, validates, and saves the shared threshold', async () => {
    render(WorkItemStalenessSettings);

    const input = await screen.findByTestId('work-item-staleness-days');
    expect(input).toHaveValue(30);

    await fireEvent.input(input, { target: { value: '45' } });
    await fireEvent.click(screen.getByTestId('work-item-staleness-save'));

    await waitFor(() => {
      expect(api.workItemStaleness.update).toHaveBeenCalledWith({ stale_after_days: 45 });
    });
    expect(workItemStalenessSettings.hydrate).toHaveBeenCalledWith({ stale_after_days: 45 });
    expect(successToast).toHaveBeenCalled();
  });

  it('keeps invalid values client-side', async () => {
    render(WorkItemStalenessSettings);

    const input = await screen.findByTestId('work-item-staleness-days');
    await fireEvent.input(input, { target: { value: '0' } });
    await fireEvent.click(screen.getByTestId('work-item-staleness-save'));

    expect(await screen.findByText(/between 1 and 365 days/i)).toBeInTheDocument();
    expect(api.workItemStaleness.update).not.toHaveBeenCalled();
  });
});
