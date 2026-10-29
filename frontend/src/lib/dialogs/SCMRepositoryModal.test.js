import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

const { getRepositories } = vi.hoisted(() => ({
  getRepositories: vi.fn(async () => [{ id: 9, repository_name: 'core', provider_name: 'GitHub' }]),
}));

vi.mock('../api/scm.js', () => ({
  itemSCMLinks: { getRepositories },
}));

import SCMRepositoryModalHarness from './SCMRepositoryModalHarness.svelte';
import { itemSCMLinks } from '../api/scm.js';

afterEach(() => {
  cleanup();
  getRepositories.mockClear();
});

describe('SCMRepositoryModal', () => {
  it('loads repositories, selects the sole repository, and submits shared content', async () => {
    render(SCMRepositoryModalHarness);

    await waitFor(() =>
      expect(screen.getByTestId('scm-modal-state')).toHaveAttribute('data-selected-repository', '9')
    );
    expect(screen.getByTestId('branch-name')).toBeInTheDocument();

    await fireEvent.click(screen.getByTestId('dialog-confirm'));
    expect(screen.getByTestId('scm-modal-state')).toHaveAttribute('data-submitted', '1');
  });
});
