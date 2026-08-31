import { render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  loadWorkspaceSCMOverview: vi.fn(),
}));

vi.mock('./workspaceSCMData.js', () => ({
  loadWorkspaceSCMOverview: mocks.loadWorkspaceSCMOverview,
}));

import WorkspaceSCMSettings from './WorkspaceSCMSettings.svelte';

function hasGitHubColorMarker(element) {
  return [...element.querySelectorAll('span')].some(
    (span) => getComputedStyle(span).backgroundColor === 'rgb(51, 51, 51)'
  );
}

describe('WorkspaceSCMSettings provider identity', () => {
  beforeEach(() => {
    mocks.loadWorkspaceSCMOverview.mockResolvedValue({
      availableProviders: [
        {
          id: 3,
          name: 'GitHub Windshift',
          provider_type: 'github',
          is_connected: true,
        },
      ],
      connections: [
        {
          id: 10,
          scm_provider_id: 3,
          provider_name: 'GitHub Windshift',
          provider_type: 'github',
          repository_count: 1,
        },
      ],
      authStatuses: {
        10: { auth_method: 'oauth', is_authenticated: true },
      },
    });
  });

  it('does not show provider colors as connection status indicators', async () => {
    render(WorkspaceSCMSettings, { props: { workspaceId: 7 } });

    const availableProvider = await screen.findByTestId('scm-provider-3');
    const connectedProvider = screen.getByTestId('scm-connection-10');

    expect(hasGitHubColorMarker(availableProvider)).toBe(false);
    expect(hasGitHubColorMarker(connectedProvider)).toBe(false);
  });
});
