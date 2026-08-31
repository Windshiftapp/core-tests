import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

beforeAll(() => {
  if (!Element.prototype.animate) {
    Element.prototype.animate = () => ({
      finished: Promise.resolve(),
      cancel: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      play: () => {},
      pause: () => {},
    });
  }
});

vi.mock('../../api.js', () => ({
  api: {
    runnerPools: {
      listWorkspaceTokens: vi.fn(),
      mintWorkspaceToken: vi.fn(),
      revokeWorkspaceToken: vi.fn(),
      listWorkspaceInstances: vi.fn(),
    },
  },
}));

import { api } from '../../api.js';
import AgentRunnerSetup from './AgentRunnerSetup.svelte';

beforeEach(() => {
  api.runnerPools.listWorkspaceInstances.mockResolvedValue([]);
  api.runnerPools.listWorkspaceTokens.mockResolvedValue([
    {
      id: 31,
      pool_capability_id: 7,
      token_prefix: 'wsrt_demo',
      expires_at: new Date(Date.now() + 60_000).toISOString(),
    },
  ]);
  api.runnerPools.mintWorkspaceToken.mockResolvedValue({
    id: 31,
    token: 'wsrt_plaintext-once',
    install_command: 'docker run --rm windshift-runner',
  });
  api.runnerPools.revokeWorkspaceToken.mockResolvedValue({
    id: 31,
    revoked: true,
  });
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('AgentRunnerSetup', () => {
  it('offers one setup path for a new runner', async () => {
    render(AgentRunnerSetup, {
      props: {
        workspaceId: 3,
        pools: [{ id: 7, name: 'Engineering runners' }],
        selectedPoolId: 7,
      },
    });

    await fireEvent.click(document.querySelector('#agent-runner-mode'));

    const options = screen.getAllByTestId('agent-runner-mode-option');
    expect(options).toHaveLength(2);
    expect(options[0]).toHaveTextContent('Use an existing runner pool');
    expect(options[1]).toHaveTextContent('Set up a new runner');
  });

  it('shows a generated command once and does not revoke on ordinary navigation', async () => {
    const view = render(AgentRunnerSetup, {
      props: {
        workspaceId: 3,
        pools: [{ id: 7, name: 'Engineering runners' }],
        selectedPoolId: 7,
        setupMode: 'new',
      },
    });

    await fireEvent.click(screen.getByTestId('agent-runner-generate'));

    await waitFor(() =>
      expect(api.runnerPools.mintWorkspaceToken).toHaveBeenCalledWith(3, 7, {
        description: 'Agent Studio · new runner',
        ttl_hours: 720,
      })
    );
    expect(screen.getByTestId('agent-runner-command')).toHaveTextContent(
      'docker run --rm windshift-runner'
    );

    view.unmount();
    expect(api.runnerPools.revokeWorkspaceToken).not.toHaveBeenCalled();
  });

  it('revokes the pending one-time token on explicit cancellation', async () => {
    render(AgentRunnerSetup, {
      props: {
        workspaceId: 3,
        pools: [{ id: 7, name: 'Engineering runners' }],
        selectedPoolId: 7,
        setupMode: 'new',
      },
    });

    await fireEvent.click(screen.getByTestId('agent-runner-generate'));
    await fireEvent.click(await screen.findByTestId('agent-runner-cancel'));

    await waitFor(() =>
      expect(api.runnerPools.revokeWorkspaceToken).toHaveBeenCalledWith(3, 7, 31)
    );
    expect(screen.queryByTestId('agent-runner-command')).not.toBeInTheDocument();
  });

  it('recognizes a fresh runner and clears the consumed command from memory', async () => {
    api.runnerPools.listWorkspaceInstances.mockResolvedValueOnce([]).mockResolvedValueOnce([
      {
        id: 88,
        name: 'new-runner',
        status: 'active',
        registered_at: new Date().toISOString(),
      },
    ]);
    render(AgentRunnerSetup, {
      props: {
        workspaceId: 3,
        pools: [{ id: 7, name: 'Engineering runners' }],
        selectedPoolId: 7,
        setupMode: 'new',
      },
    });

    await fireEvent.click(screen.getByTestId('agent-runner-generate'));

    expect(await screen.findByTestId('agent-runner-ready')).toHaveTextContent('new-runner');
    expect(screen.queryByTestId('agent-runner-command')).not.toBeInTheDocument();
    expect(api.runnerPools.revokeWorkspaceToken).not.toHaveBeenCalled();
  });
});
