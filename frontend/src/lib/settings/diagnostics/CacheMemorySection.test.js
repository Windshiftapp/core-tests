import { cleanup, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('../../api/diagnostics.js', () => ({
  getCacheMemory: vi.fn(),
}));

import { getCacheMemory } from '../../api/diagnostics.js';
import CacheMemorySection from './CacheMemorySection.svelte';

describe('CacheMemorySection', () => {
  beforeEach(() => {
    vi.mocked(getCacheMemory).mockResolvedValue({
      healthy: true,
      budget: { process_limit_mb: 2048, go_limit_bytes: 1717986918 },
      allocated_cache_bytes: 10485760,
      maximum_cache_bytes: 536870912,
      caches: [
        {
          name: 'permissions',
          entries: 12,
          allocated_capacity_bytes: 4194304,
          maximum_capacity_bytes: 128974848,
          hits: 40,
          misses: 2,
          no_space_evictions: 0,
        },
      ],
    });
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  test('renders process budget and cache counters', async () => {
    render(CacheMemorySection);
    await waitFor(() => expect(getCacheMemory).toHaveBeenCalledTimes(1));
    expect(screen.getByText('2048 MiB')).toBeInTheDocument();
    expect(screen.getByText('permissions')).toBeInTheDocument();
    expect(screen.getByText('40 / 2')).toBeInTheDocument();
    expect(screen.getByText('Stable')).toBeInTheDocument();
  });
});
