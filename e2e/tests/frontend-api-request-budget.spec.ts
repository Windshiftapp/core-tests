import type { APIRequestContext, Page, Request, TestInfo } from '@playwright/test';
import {
  createCollectionViaAPI,
  createItemViaAPI,
  createTeamViaAPI,
  createUserViaAPI,
  createWorkspaceViaAPI,
} from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/errors';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

interface RouteBudget {
  coldRequests: number;
  warmRequests: number;
  coldBytes: number;
  warmBytes: number;
}

interface ReadySurface {
  testId: string;
  readyAttribute?: boolean;
  verify?: (page: Page) => Promise<void>;
}

interface RouteMeasurement {
  requests: string[];
  transferBytes: number;
}

const AUTHENTICATED_SHELL_BUDGET: RouteBudget = {
  coldRequests: 60,
  warmRequests: 60,
  coldBytes: 384 * 1024,
  warmBytes: 384 * 1024,
};

const PUBLIC_PORTAL_BUDGET: RouteBudget = {
  coldRequests: 8,
  warmRequests: 8,
  coldBytes: 128 * 1024,
  warmBytes: 128 * 1024,
};

const PUBLIC_FORM_BUDGET: RouteBudget = {
  coldRequests: 3,
  warmRequests: 3,
  coldBytes: 128 * 1024,
  warmBytes: 128 * 1024,
};

function normalizeAPIPath(rawUrl: string): string | null {
  const url = new URL(rawUrl);
  const apiIndex = url.pathname.indexOf('/api/');
  if (apiIndex === -1) return null;

  const params = [...url.searchParams.entries()].sort(
    ([leftKey, leftValue], [rightKey, rightValue]) => {
      const keyOrder = leftKey.localeCompare(rightKey);
      return keyOrder === 0 ? leftValue.localeCompare(rightValue) : keyOrder;
    }
  );
  const query = new URLSearchParams(params).toString();
  const path = url.pathname.slice(apiIndex);
  return query ? `${path}?${query}` : path;
}

function exactDuplicates(paths: string[]): string[] {
  const counts = new Map<string, number>();
  for (const path of paths) counts.set(path, (counts.get(path) ?? 0) + 1);
  return [...counts.entries()]
    .filter(([, count]) => count > 1)
    .map(([path, count]) => `${path} × ${count}`)
    .sort();
}

async function waitForSurface(page: Page, ready: ReadySurface): Promise<void> {
  const surface = page.getByTestId(ready.testId);
  await expect(surface).toBeVisible();
  if (ready.readyAttribute) {
    await expect(surface).toHaveAttribute('data-ready', 'true');
  }
  await ready.verify?.(page);
}

async function measureNavigation(
  page: Page,
  navigate: () => Promise<unknown>,
  ready: ReadySurface
): Promise<RouteMeasurement> {
  const requests: string[] = [];
  const onRequest = (browserRequest: Request) => {
    if (browserRequest.method() !== 'GET' || browserRequest.resourceType() === 'eventsource') {
      return;
    }
    const path = normalizeAPIPath(browserRequest.url());
    if (path) requests.push(path);
  };

  page.on('request', onRequest);
  try {
    await navigate();
    await waitForSurface(page, ready);
  } finally {
    page.off('request', onRequest);
  }

  const transferBytes = await page.evaluate(() =>
    performance
      .getEntriesByType('resource')
      .filter((entry) => entry.name.includes('/api/'))
      .reduce((total, entry) => total + (entry as PerformanceResourceTiming).transferSize, 0)
  );

  return { requests, transferBytes };
}

function assertMeasurement(
  label: string,
  measurement: RouteMeasurement,
  maxRequests: number,
  maxBytes: number
): void {
  expect(
    exactDuplicates(measurement.requests),
    `${label} duplicate normalized GETs: ${measurement.requests.join(', ')}`
  ).toEqual([]);
  expect(
    measurement.requests.length,
    `${label} GETs: ${measurement.requests.join(', ')}`
  ).toBeLessThanOrEqual(maxRequests);
  expect(measurement.transferBytes, `${label} API transfer bytes`).toBeLessThanOrEqual(maxBytes);
}

async function assertColdAndWarmBudget(
  page: Page,
  testInfo: TestInfo,
  label: string,
  path: string,
  ready: ReadySurface,
  budget: RouteBudget
): Promise<{ cold: RouteMeasurement; warm: RouteMeasurement }> {
  const cold = await measureNavigation(page, () => page.goto(path), ready);
  const warm = await measureNavigation(page, () => page.reload(), ready);

  await testInfo.attach(`${label}-request-budget.json`, {
    body: JSON.stringify({ path, budget, cold, warm }, null, 2),
    contentType: 'application/json',
  });

  assertMeasurement(`${label} cold`, cold, budget.coldRequests, budget.coldBytes);
  assertMeasurement(`${label} warm`, warm, budget.warmRequests, budget.warmBytes);
  return { cold, warm };
}

function uniqueSuffix(testInfo: TestInfo, label: string): string {
  return `${label}-${Date.now().toString(36)}-${testInfo.workerIndex}-${testInfo.retry}`;
}

function shortKey(value: string): string {
  let hash = 0;
  for (const character of value) {
    hash = (Math.imul(hash, 31) + character.charCodeAt(0)) >>> 0;
  }
  return `RB${hash.toString(36).toUpperCase().padStart(7, '0')}`.slice(0, 10);
}

async function expectOK(response: Awaited<ReturnType<APIRequestContext['get']>>, label: string) {
  const body = await response.text();
  expect(response.ok(), `${label}: ${response.status()} ${body}`).toBeTruthy();
  return body ? JSON.parse(body) : null;
}

async function createPublicRequestChannel(
  request: APIRequestContext,
  type: 'portal' | 'form',
  slug: string,
  name: string
): Promise<{ channelId: number; workspaceId: number; requestTypeId: number }> {
  const workspace = await createWorkspaceViaAPI(request, {
    name: `${name} Workspace`,
    key: shortKey(slug),
    description: `${type} request-budget workspace`,
  });

  const channel = await expectOK(
    await request.post('/api/channels', {
      headers: SEC_FETCH,
      data: {
        name,
        type,
        direction: 'inbound',
        slug,
      },
    }),
    `create ${type} channel`
  );

  const workspaceConfig =
    type === 'portal'
      ? {
          portal_workspace_ids: [workspace.id],
          portal_title: name,
          portal_registration_mode: 'open',
        }
      : {
          form_workspace_ids: [workspace.id],
          form_theme: 'light',
        };
  await expectOK(
    await request.put(`/api/channels/${channel.id}/config`, {
      headers: SEC_FETCH,
      data: { config: workspaceConfig },
    }),
    `configure ${type} channel`
  );

  const requestType = await expectOK(
    await request.post(`/api/channels/${channel.id}/request-types`, {
      headers: SEC_FETCH,
      data: {
        name: `${name} Request`,
        item_type_id: 1,
        workspace_id: workspace.id,
        is_active: true,
      },
    }),
    `create ${type} request type`
  );
  await expectOK(
    await request.put(`/api/channels/${channel.id}/request-types/${requestType.id}/fields`, {
      headers: SEC_FETCH,
      data: [
        {
          field_identifier: 'title',
          field_type: 'default',
          display_order: 0,
          is_required: true,
          step_number: 1,
        },
        {
          field_identifier: 'description',
          field_type: 'default',
          display_order: 1,
          is_required: false,
          step_number: 1,
        },
      ],
    }),
    `configure ${type} request fields`
  );

  if (type === 'portal') {
    await expectOK(
      await request.put(`/api/channels/${channel.id}/config`, {
        headers: SEC_FETCH,
        data: {
          config: {
            portal_sections: [
              {
                id: `request-budget-${channel.id}`,
                title: '',
                subtitle: '',
                display_order: 0,
                request_type_ids: [requestType.id],
                asset_report_ids: [],
              },
            ],
          },
        },
      }),
      'configure portal section'
    );
  }

  await expectOK(
    await request.put(`/api/channels/${channel.id}/toggle`, {
      headers: SEC_FETCH,
    }),
    `enable ${type} channel`
  );

  return {
    channelId: channel.id,
    workspaceId: workspace.id,
    requestTypeId: requestType.id,
  };
}

async function createTestRunWithCases(
  request: APIRequestContext,
  workspaceId: number,
  suffix: string,
  count: number
): Promise<number> {
  const testSet = await expectOK(
    await request.post(`/api/workspaces/${workspaceId}/test-sets`, {
      headers: SEC_FETCH,
      data: { name: `Budget Set ${suffix}`, description: '' },
    }),
    'create test set'
  );

  for (let index = 0; index < count; index += 1) {
    const testCase = await expectOK(
      await request.post(`/api/workspaces/${workspaceId}/test-cases`, {
        headers: SEC_FETCH,
        data: {
          title: `Budget Case ${index + 1} ${suffix}`,
          preconditions: '',
          priority: 'medium',
          status: 'active',
          estimated_duration: 0,
        },
      }),
      `create test case ${index + 1}`
    );
    await expectOK(
      await request.post(`/api/workspaces/${workspaceId}/test-cases/${testCase.id}/steps`, {
        headers: SEC_FETCH,
        data: {
          action: `Perform action ${index + 1}`,
          data: '',
          expected: 'Action succeeds',
        },
      }),
      `create test step ${index + 1}`
    );
    await expectOK(
      await request.post(`/api/workspaces/${workspaceId}/test-sets/${testSet.id}/test-cases`, {
        headers: SEC_FETCH,
        data: { test_case_id: testCase.id },
      }),
      `link test case ${index + 1}`
    );
  }

  const run = await expectOK(
    await request.post(`/api/workspaces/${workspaceId}/test-runs`, {
      headers: SEC_FETCH,
      data: { name: `Budget Run ${suffix}`, set_id: testSet.id },
    }),
    'create test run'
  );
  return run.id;
}

test.describe('Frontend API request budgets (WI-689)', () => {
  const collectionViews = [
    { route: 'board', testId: 'board-view' },
    { route: 'list', testId: 'list-view' },
    { route: 'tree', testId: 'tree-view' },
    { route: 'map', testId: 'map-view' },
    { route: 'roadmap', testId: 'roadmap-view' },
  ] as const;

  for (const view of collectionViews) {
    test(`workspace ${view.route} cold/warm request graph`, async ({ page, request }, testInfo) => {
      const suffix = uniqueSuffix(testInfo, view.route);
      const workspace = await createWorkspaceViaAPI(request, {
        name: `Budget ${suffix}`,
        key: `B${view.route[0]}${Date.now().toString(36).slice(-6)}`.toUpperCase().slice(0, 10),
        description: `WI-689 ${view.route} request budget`,
      });
      await createItemViaAPI(request, workspace.id, {
        title: `Budget item ${suffix}`,
      });
      const collection = await createCollectionViaAPI(request, {
        name: `Budget collection ${suffix}`,
        workspace_id: workspace.id,
      });
      await expectOK(
        await request.post(`/api/collections/${collection.id}/board-configuration`, {
          headers: SEC_FETCH,
          data: {
            columns: [],
            backlog_status_ids: [],
            list_columns: [],
            card_fields: [],
            show_rightmost_column_last_50: false,
          },
        }),
        `create ${view.route} board configuration`
      );

      const result = await assertColdAndWarmBudget(
        page,
        testInfo,
        `workspace-${view.route}`,
        `/workspaces/${workspace.id}/collections/${collection.id}/${view.route}`,
        { testId: view.testId },
        AUTHENTICATED_SHELL_BUDGET
      );

      const bootstrapPath = `/api/workspaces/${workspace.id}/bootstrap`;
      expect(result.cold.requests.filter((path) => path === bootstrapPath)).toHaveLength(1);
      expect(result.warm.requests.filter((path) => path === bootstrapPath)).toHaveLength(1);
    });
  }

  test('global collection board leaves workflow validation to transition requests', async ({
    page,
    request,
  }, testInfo) => {
    const suffix = uniqueSuffix(testInfo, 'global-board-transitions');
    const title = `Global transition budget ${suffix}`;
    const workspace = await createWorkspaceViaAPI(request, {
      name: `Global transition ${suffix}`,
      key: `GT${Date.now().toString(36).slice(-6)}`.toUpperCase().slice(0, 10),
      description: 'Global collection transition request regression',
    });
    await createItemViaAPI(request, workspace.id, { title });
    const collection = await createCollectionViaAPI(request, {
      name: `Global transition ${suffix}`,
      ql_query: `title ~ "${title}"`,
    });
    await expectOK(
      await request.post(`/api/collections/${collection.id}/board-configuration`, {
        headers: SEC_FETCH,
        data: {
          columns: [],
          backlog_status_ids: [],
          list_columns: [],
          card_fields: [],
          show_rightmost_column_last_50: false,
        },
      }),
      'create global collection board configuration'
    );

    const result = await assertColdAndWarmBudget(
      page,
      testInfo,
      'global-board-transitions',
      `/collections/${collection.id}/board`,
      { testId: 'board-view' },
      AUTHENTICATED_SHELL_BUDGET
    );

    for (const measurement of [result.cold, result.warm]) {
      expect(
        measurement.requests.filter((path) => path.endsWith('/available-status-transitions'))
      ).toHaveLength(0);
      expect(
        measurement.requests.filter(
          (path) => path === `/api/workspaces/${workspace.id}/transition-matrix`
        )
      ).toHaveLength(0);
    }
  });

  test('homepage cold/warm request graph', async ({ page }, testInfo) => {
    const result = await assertColdAndWarmBudget(
      page,
      testInfo,
      'homepage',
      '/',
      { testId: 'homepage', readyAttribute: true },
      AUTHENTICATED_SHELL_BUDGET
    );

    expect(result.cold.requests.filter((path) => path === '/api/homepage')).toHaveLength(1);
    expect(result.warm.requests.filter((path) => path === '/api/homepage')).toHaveLength(1);
  });

  for (const authenticated of [true, false]) {
    test(`${authenticated ? 'authenticated' : 'anonymous'} portal cold/warm request graph`, async ({
      page,
      request,
      allowConsoleError,
    }, testInfo) => {
      const suffix = uniqueSuffix(testInfo, authenticated ? 'portal-auth' : 'portal-anon');
      const slug = suffix.slice(0, 60);
      await createPublicRequestChannel(request, 'portal', slug, `Portal ${suffix}`);
      if (!authenticated) {
        allowConsoleError(/\/api\/auth\/me/);
        await page.context().clearCookies();
      }

      const result = await assertColdAndWarmBudget(
        page,
        testInfo,
        authenticated ? 'portal-authenticated' : 'portal-anonymous',
        `/portal/${slug}`,
        { testId: 'portal-page', readyAttribute: true },
        PUBLIC_PORTAL_BUDGET
      );

      const bootstrapPath = `/api/portal/${slug}/bootstrap`;
      expect(result.cold.requests.filter((path) => path === bootstrapPath)).toHaveLength(1);
      expect(result.warm.requests.filter((path) => path === bootstrapPath)).toHaveLength(1);
    });
  }

  test('sole-form public entry cold/warm request graph', async ({
    page,
    request,
    allowConsoleError,
  }, testInfo) => {
    const suffix = uniqueSuffix(testInfo, 'form');
    const slug = suffix.slice(0, 60);
    await createPublicRequestChannel(request, 'form', slug, `Form ${suffix}`);
    allowConsoleError(/\/api\/auth\/me/);
    await page.context().clearCookies();

    const result = await assertColdAndWarmBudget(
      page,
      testInfo,
      'sole-public-form',
      `/forms/${slug}`,
      { testId: 'public-form-page', readyAttribute: true },
      PUBLIC_FORM_BUDGET
    );

    const bootstrapPath = `/api/forms/${slug}/bootstrap`;
    expect(result.cold.requests.filter((path) => path === bootstrapPath)).toHaveLength(1);
    expect(result.warm.requests.filter((path) => path === bootstrapPath)).toHaveLength(1);
  });

  test('mobile item detail cold/warm request graph keeps heavy panels deferred', async ({
    page,
    request,
    allowConsoleError,
  }, testInfo) => {
    allowConsoleError(/\/api\/logbook\//);
    allowConsoleError(/\/api\/items\/\d+\/recurrence/);

    const suffix = uniqueSuffix(testInfo, 'mobile-item');
    const workspace = await createWorkspaceViaAPI(request, {
      name: `Mobile Budget ${suffix}`,
      key: `MB${Date.now().toString(36).slice(-6)}`.toUpperCase(),
      description: 'WI-689 mobile item request budget',
    });
    const item = await createItemViaAPI(request, workspace.id, {
      title: `Mobile budget item ${suffix}`,
    });

    const result = await assertColdAndWarmBudget(
      page,
      testInfo,
      'mobile-item-detail',
      `/m/items/${item.id}`,
      { testId: 'mobile-item-detail' },
      AUTHENTICATED_SHELL_BUDGET
    );

    for (const measurement of [result.cold, result.warm]) {
      expect(measurement.requests).not.toContain(`/api/items/${item.id}/diagrams`);
      expect(measurement.requests).not.toContain(`/api/items/${item.id}/worklogs`);
    }
  });

  test('Permission Manager uses bulk assignments with multiple users', async ({
    page,
    request,
  }, testInfo) => {
    const suffix = uniqueSuffix(testInfo, 'permissions').replace(/[^a-z0-9]/gi, '');
    for (let index = 0; index < 3; index += 1) {
      const username = `rb${index}${suffix}`.slice(0, 30);
      await createUserViaAPI(request, {
        email: `${username}@windshift.test`,
        username,
        first_name: 'Request',
        last_name: `Budget ${index + 1}`,
        password_hash: 'E2e-request-budget-123!',
      });
    }

    const result = await assertColdAndWarmBudget(
      page,
      testInfo,
      'permission-manager',
      '/admin/permissions',
      {
        testId: 'permission-manager',
        readyAttribute: true,
        verify: async (currentPage) => {
          await expect
            .poll(async () =>
              Number(
                await currentPage.getByTestId('permission-manager').getAttribute('data-user-count')
              )
            )
            .toBeGreaterThanOrEqual(3);
        },
      },
      AUTHENTICATED_SHELL_BUDGET
    );

    for (const measurement of [result.cold, result.warm]) {
      expect(measurement.requests).toContain('/api/users/permissions/global');
      // The authenticated shell loads the current user's permission profile
      // once. Permission Manager must not add one request per listed user.
      expect(
        measurement.requests.filter((path) => /\/api\/users\/\d+\/permissions/.test(path))
      ).toHaveLength(1);
    }
  });

  test('Test Execution uses one aggregate with multiple cases', async ({
    page,
    request,
  }, testInfo) => {
    const suffix = uniqueSuffix(testInfo, 'execution');
    const workspace = await createWorkspaceViaAPI(request, {
      name: `Execution Budget ${suffix}`,
      key: `TE${Date.now().toString(36).slice(-6)}`.toUpperCase(),
      description: 'WI-689 test execution request budget',
    });
    const runId = await createTestRunWithCases(request, workspace.id, suffix, 3);

    const result = await assertColdAndWarmBudget(
      page,
      testInfo,
      'test-execution',
      `/workspaces/${workspace.id}/tests/runs/${runId}/execute`,
      {
        testId: 'test-execution',
        verify: async (currentPage) => {
          await expect(currentPage.getByTestId(/^test-execution-case-\d+$/)).toHaveCount(3);
        },
      },
      AUTHENTICATED_SHELL_BUDGET
    );

    const detailPath = `/api/workspaces/${workspace.id}/test-runs/${runId}/detail`;
    for (const measurement of [result.cold, result.warm]) {
      expect(measurement.requests.filter((path) => path === detailPath)).toHaveLength(1);
      expect(
        measurement.requests.some((path) =>
          new RegExp(`^/api/workspaces/${workspace.id}/test-cases/\\d+/steps`).test(path)
        )
      ).toBe(false);
    }
  });

  test('On-call uses one aggregate with multiple schedules', async ({
    page,
    request,
  }, testInfo) => {
    const suffix = uniqueSuffix(testInfo, 'on-call');
    const team = await createTeamViaAPI(request, {
      name: `On-call Budget ${suffix}`,
      description: 'WI-689 on-call request budget',
    });
    for (let index = 0; index < 3; index += 1) {
      await expectOK(
        await request.post(`/api/teams/${team.id}/on-call/schedules`, {
          headers: SEC_FETCH,
          data: {
            name: `Schedule ${index + 1} ${suffix}`,
            description: '',
            timezone: 'UTC',
          },
        }),
        `create schedule ${index + 1}`
      );
    }

    const result = await assertColdAndWarmBudget(
      page,
      testInfo,
      'on-call',
      `/teams/${team.id}/on-call`,
      {
        testId: 'on-call-tab',
        readyAttribute: true,
        verify: async (currentPage) => {
          await expect(currentPage.getByTestId('schedule-row')).toHaveCount(3);
        },
      },
      AUTHENTICATED_SHELL_BUDGET
    );

    const schedulesPath = `/api/teams/${team.id}/on-call/schedules`;
    for (const measurement of [result.cold, result.warm]) {
      expect(measurement.requests.filter((path) => path === schedulesPath)).toHaveLength(1);
      expect(
        measurement.requests.some((path) => /^\/api\/on-call\/schedules\/\d+/.test(path))
      ).toBe(false);
    }
  });
});
