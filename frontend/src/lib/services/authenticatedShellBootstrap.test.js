import { describe, expect, test, vi } from 'vitest';
import {
  AUTHENTICATED_SHELL_BOOTSTRAP_BUDGET_MS,
  runAuthenticatedShellBootstrap,
} from './authenticatedShellBootstrap.js';

describe('authenticated shell bootstrap', () => {
  test('starts deferred probes without waiting for the critical path', async () => {
    let releaseCritical;
    const events = [];
    const critical = new Promise((resolve) => {
      releaseCritical = resolve;
    });

    const bootstrap = runAuthenticatedShellBootstrap({
      userId: 7,
      criticalTasks: [
        () => {
          events.push('critical');
          return critical;
        },
      ],
      deferredTasks: [
        () => {
          events.push('deferred');
        },
      ],
    });

    await vi.waitFor(() => expect(events).toEqual(['critical', 'deferred']));
    releaseCritical();
    const result = await bootstrap;
    await result.deferredPromise;

    expect(result.metrics.criticalRequestCount).toBe(1);
    expect(result.metrics.deferredRequestCount).toBe(1);
    expect(result.metrics.criticalDurationMs).toBeLessThan(AUTHENTICATED_SHELL_BOOTSTRAP_BUDGET_MS);
    expect(result.metrics.withinBudget).toBe(true);
  });

  test('settles optional failures without rejecting shell readiness', async () => {
    const result = await runAuthenticatedShellBootstrap({
      userId: 8,
      criticalTasks: [() => Promise.resolve('ready')],
      deferredTasks: [() => Promise.reject(new Error('feature disabled'))],
    });

    await expect(result.deferredPromise).resolves.toEqual([
      expect.objectContaining({ status: 'rejected' }),
    ]);
    expect(result.criticalResults).toEqual([
      expect.objectContaining({ status: 'fulfilled', value: 'ready' }),
    ]);
  });
});
