import { cleanup, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('../../api/diagnostics.js', () => ({
  getDatabasePools: vi.fn(),
}));

import { getDatabasePools } from '../../api/diagnostics.js';
import DatabasePoolsSection from './DatabasePoolsSection.svelte';

describe('DatabasePoolsSection', () => {
  beforeEach(() => {
    vi.mocked(getDatabasePools).mockResolvedValue({
      instance: 'windshift-1',
      sampled_at: '2026-07-16T07:00:00Z',
      healthy: false,
      capacity: {
        required_connections: 115,
        server_max_connections: 100,
        utilization_percent: 115,
        replica_count: 3,
        connections_per_replica: 35,
        headroom_connections: 10,
        safe: false,
      },
      process: {
        goroutines: 42,
        heap_alloc_bytes: 1048576,
        system_bytes: 2097152,
      },
      pools: [
        {
          name: 'main',
          driver: 'postgres',
          max_open_connections: 30,
          open_connections: 30,
          in_use: 30,
          idle: 0,
          wait_count: 7,
          wait_duration_ms: 250,
          max_idle_closed: 1,
          max_idle_time_closed: 2,
          max_lifetime_closed: 3,
          utilization_percent: 100,
          saturated: true,
        },
      ],
    });
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  test('renders local pool, runtime, budget, and saturation state', async () => {
    render(DatabasePoolsSection);

    await waitFor(() => expect(getDatabasePools).toHaveBeenCalledTimes(1));
    expect(screen.getByTestId('database-pools-alert')).toBeInTheDocument();
    expect(screen.getByText('windshift-1')).toBeInTheDocument();
    expect(screen.getByText('Saturated')).toBeInTheDocument();
    expect(screen.getByText('115 / 100 connections (115.0%)')).toBeInTheDocument();
    expect(screen.getByText('42')).toBeInTheDocument();
    expect(screen.getByText('1.0 MiB')).toBeInTheDocument();
  });
});
