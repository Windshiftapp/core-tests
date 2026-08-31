import { cleanup, render, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('../../api/diagnostics.js', () => ({
  getSCMConnectionHealth: vi.fn(),
}));

import { getSCMConnectionHealth } from '../../api/diagnostics.js';
import SCMHealthSection from './SCMHealthSection.svelte';

const operation = (overrides = {}) => ({
  operation: 'repository_sync',
  state: 'healthy',
  healthy: true,
  last_attempt_at: '2026-08-27T20:00:00Z',
  last_success_at: '2026-08-27T20:00:00Z',
  consecutive_failures: 0,
  checked_resources: 2,
  failed_resources: 0,
  last_error: '',
  ...overrides,
});

describe('SCMHealthSection', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => cleanup());

  test('prioritizes unhealthy connection details and renders healthy operations', async () => {
    getSCMConnectionHealth.mockResolvedValue([
      {
        id: 7,
        workspace_name: 'Platform',
        workspace_key: 'PLAT',
        provider_name: 'GitHub',
        provider_slug: 'github-production',
        provider_base_url: 'https://github.example.test/api/v3',
        provider_type: 'github',
        auth_method: 'github_app',
        enabled: true,
        repository_count: 2,
        active_repository_count: 2,
        repositories: [
          {
            id: 21,
            name: 'acme/platform',
            url: 'https://github.example.test/acme/platform',
            active: true,
          },
          {
            id: 22,
            name: 'acme/agents',
            url: 'https://github.example.test/acme/agents',
            active: true,
          },
        ],
        state: 'unhealthy',
        healthy: false,
        operations: [
          operation(),
          operation({
            operation: 'pull_request_refresh',
            state: 'unhealthy',
            healthy: false,
            last_success_at: null,
            last_failure_at: '2026-08-27T20:15:00Z',
            consecutive_failures: 3,
            checked_resources: 1,
            failed_resources: 1,
            last_error: 'repository acme/platform PR #42: failed to get PR: resource not found',
          }),
        ],
      },
    ]);

    const view = render(SCMHealthSection);
    await waitFor(() => expect(getSCMConnectionHealth).toHaveBeenCalledTimes(1));
    expect(view.getByTestId('scm-health-alert')).toHaveTextContent('Platform');
    const connection = view.getByTestId('scm-health-connection-7');
    expect(connection).toHaveTextContent('Connection #7');
    expect(connection).toHaveTextContent('github-production');
    expect(connection).toHaveTextContent('github.example.test');
    expect(connection).toHaveTextContent('acme/platform');
    expect(
      view.getByText('repository acme/platform PR #42: failed to get PR: resource not found')
    ).toBeInTheDocument();
    expect(view.getByText('Repository sync')).toBeInTheDocument();
    expect(view.getByText('Pull request refresh')).toBeInTheDocument();
    expect(view.getByText('3 consecutive')).toBeInTheDocument();
  });

  test('renders an empty state', async () => {
    getSCMConnectionHealth.mockResolvedValue([]);
    const view = render(SCMHealthSection);
    await waitFor(() => expect(getSCMConnectionHealth).toHaveBeenCalledTimes(1));
    expect(view.getByText('No SCM connections configured.')).toBeInTheDocument();
  });

  test('distinguishes recovered, disabled, and never-checked connections', async () => {
    getSCMConnectionHealth.mockResolvedValue([
      {
        id: 8,
        workspace_name: 'Recovered',
        workspace_key: 'REC',
        provider_name: 'GitHub',
        provider_type: 'github',
        auth_method: 'pat',
        enabled: true,
        repository_count: 1,
        active_repository_count: 1,
        state: 'healthy',
        healthy: true,
        operations: [operation(), operation({ operation: 'pull_request_refresh' })],
      },
      {
        id: 9,
        workspace_name: 'Paused',
        workspace_key: 'PAUSE',
        provider_name: 'Gitea',
        provider_type: 'gitea',
        auth_method: 'pat',
        enabled: false,
        repository_count: 0,
        active_repository_count: 0,
        state: 'disabled',
        healthy: false,
        operations: [],
      },
      {
        id: 10,
        workspace_name: 'New',
        workspace_key: 'NEW',
        provider_name: 'GitHub',
        provider_type: 'github',
        auth_method: 'github_app',
        enabled: true,
        repository_count: 0,
        active_repository_count: 0,
        state: 'never_checked',
        healthy: false,
        operations: [],
      },
    ]);

    const view = render(SCMHealthSection);
    await waitFor(() => expect(getSCMConnectionHealth).toHaveBeenCalledTimes(1));
    expect(view.getByTestId('scm-health-connection-8')).toHaveTextContent('Healthy');
    expect(view.getByTestId('scm-health-connection-9')).toHaveTextContent('Disabled');
    expect(view.getByTestId('scm-health-connection-10')).toHaveTextContent('Never checked');
  });

  test('shows its loading state while health is pending', () => {
    getSCMConnectionHealth.mockReturnValue(new Promise(() => {}));
    const view = render(SCMHealthSection);
    expect(view.getByText('Refreshing…')).toBeInTheDocument();
  });

  test('renders request failures', async () => {
    getSCMConnectionHealth.mockRejectedValue(new Error('diagnostics unavailable'));
    const view = render(SCMHealthSection);
    await waitFor(() => expect(view.getByText('diagnostics unavailable')).toBeInTheDocument());
  });
});
