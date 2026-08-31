import type { Request, TestInfo } from '@playwright/test';
import { createItemViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/errors';

// Baseline measured on the production build on 2026-07-15. The total budget
// covers the whole cold application shell, while the tighter item budget keeps
// shell traffic from hiding regressions in the item-detail request graph.
const MAX_COLD_API_REQUESTS = 60;
// A configured SCM connection adds one conditional /scm-links request. Keep
// the full-suite, feature-enabled graph deterministic while the duplicate and
// known-deferred assertions below continue to guard accidental regressions.
const MAX_ITEM_DETAIL_REQUESTS = 16;
const MAX_COLD_API_TRANSFER_BYTES = 256 * 1024;
const MAX_INTERACTIVE_P95_MS = 3_500;
// Twenty samples make the percentile meaningful and allow one isolated CI
// scheduler outlier while retaining a tight shared-runner interaction ceiling.
const INTERACTIVE_SAMPLES = 20;

function apiPath(rawUrl: string) {
  const url = new URL(rawUrl);
  const apiIndex = url.pathname.indexOf('/api/');
  return apiIndex === -1 ? null : `${url.pathname.slice(apiIndex)}${url.search}`;
}

function percentile95(samples: number[]) {
  const sorted = [...samples].sort((a, b) => a - b);
  return sorted[Math.ceil(sorted.length * 0.95) - 1];
}

function uniqueWorkspaceKey(testInfo: TestInfo) {
  const value = `${Date.now()}-${testInfo.workerIndex}-${testInfo.repeatEachIndex}-${testInfo.retry}`;
  let hash = 0;
  for (const character of value) {
    hash = (Math.imul(hash, 31) + character.charCodeAt(0)) >>> 0;
  }
  return `IDB${hash.toString(36).toUpperCase().padStart(7, '0')}`.slice(0, 10);
}

test.describe('Item detail request budget (WI-617)', () => {
  test('cold and warm opens stay within request, transfer, and interactive budgets', async ({
    page,
    request,
    allowConsoleError,
  }, testInfo) => {
    test.setTimeout(90_000);
    allowConsoleError(/\/api\/logbook\//);
    allowConsoleError(/\/api\/items\/\d+\/recurrence/);

    const stamp = Date.now();
    const workspace = await createWorkspaceViaAPI(request, {
      name: `detail-budget-${stamp}`,
      key: uniqueWorkspaceKey(testInfo),
      description: 'WI-617 item-detail request budget',
    });
    const item = await createItemViaAPI(request, workspace.id, {
      title: `Request budget ${stamp}`,
    });

    const itemPath = `/api/items/${item.id}`;
    const coldRequests: string[] = [];
    const pendingAPIRequests = new Set<Request>();
    let measuringCold = true;
    page.on('request', (browserRequest) => {
      const path = apiPath(browserRequest.url());
      if (!path || browserRequest.method() !== 'GET') return;
      if (measuringCold) coldRequests.push(path);
      if (!path.startsWith(`${itemPath}/events`)) pendingAPIRequests.add(browserRequest);
    });
    page.on('requestfinished', (browserRequest) => pendingAPIRequests.delete(browserRequest));
    page.on('requestfailed', (browserRequest) => pendingAPIRequests.delete(browserRequest));

    const waitForAPIRequestsToSettle = async () => {
      await expect.poll(() => pendingAPIRequests.size, { timeout: 10_000 }).toBe(0);
    };

    const interactiveSamples: number[] = [];
    const diagramsResponse = page.waitForResponse(
      (response) =>
        response.request().method() === 'GET' &&
        response.url().includes(`${itemPath}/diagrams`) &&
        response.ok(),
      { timeout: 10_000 }
    );
    const coldStartedAt = performance.now();
    await page.goto(`/workspaces/${workspace.id}/items/${item.id}`);
    await expect(page.getByTestId('item-detail-ready')).toBeVisible();
    interactiveSamples.push(performance.now() - coldStartedAt);

    // Capture requests started immediately after the above-the-fold surface is
    // ready, then freeze the cold graph after the deferred diagrams request
    // that belongs to this route has completed.
    await diagramsResponse;
    await waitForAPIRequestsToSettle();
    measuringCold = false;

    const transferredBytes = await page.evaluate(() =>
      performance
        .getEntriesByType('resource')
        .filter((entry) => entry.name.includes('/api/'))
        .reduce((total, entry) => total + (entry as PerformanceResourceTiming).transferSize, 0)
    );

    const itemRequests = coldRequests.filter(
      (path) =>
        (path === itemPath || path.startsWith(`${itemPath}/`)) &&
        !path.startsWith(`${itemPath}/personal-tasks`)
    );
    const nonStreamItemRequests = itemRequests.filter(
      (path) => !path.startsWith(`${itemPath}/events`)
    );
    const duplicates = nonStreamItemRequests.filter(
      (path, index, paths) => paths.indexOf(path) !== index
    );

    expect(coldRequests.filter((path) => path === `${itemPath}/detail-summary`)).toHaveLength(1);
    expect(coldRequests.filter((path) => path === itemPath)).toHaveLength(0);
    expect(duplicates).toEqual([]);
    expect(coldRequests.filter((path) => path === `${itemPath}/diagrams`)).toHaveLength(1);
    expect(coldRequests.some((path) => path === `${itemPath}/worklogs`)).toBe(false);
    expect(coldRequests.some((path) => path.startsWith('/api/customer-organisations'))).toBe(false);
    expect(itemRequests.length).toBeLessThanOrEqual(MAX_ITEM_DETAIL_REQUESTS);
    expect(coldRequests.length).toBeLessThanOrEqual(MAX_COLD_API_REQUESTS);
    expect(transferredBytes).toBeLessThanOrEqual(MAX_COLD_API_TRANSFER_BYTES);

    for (let index = 1; index < INTERACTIVE_SAMPLES; index += 1) {
      const diagramsReloaded = page.waitForResponse(
        (response) =>
          response.request().method() === 'GET' && response.url().includes(`${itemPath}/diagrams`),
        { timeout: 10_000 }
      );
      const startedAt = performance.now();
      await page.reload();
      await expect(page.getByTestId('item-detail-ready')).toBeVisible();
      interactiveSamples.push(performance.now() - startedAt);
      await diagramsReloaded;
      await waitForAPIRequestsToSettle();
    }
    expect(
      percentile95(interactiveSamples),
      `interactive samples: ${interactiveSamples.map((sample) => Math.round(sample)).join(', ')}ms`
    ).toBeLessThanOrEqual(MAX_INTERACTIVE_P95_MS);
  });
});
