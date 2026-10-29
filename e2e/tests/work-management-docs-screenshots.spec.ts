import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { resolve } from 'node:path';

// Work-management captures (milestones, iterations, teams) for the website
// documentation. Seeds its own workspace so the admin screenshot spec stays
// independent.
const enabled = process.env.DOCS_SCREENSHOTS === '1';
const outputDir = resolve(
  process.env.DOCS_SCREENSHOTS_DIR || '../../../windshift-website/public/assets/docs'
);
const headers = { 'Sec-Fetch-Site': 'same-origin' };

test.skip(!enabled, 'Set DOCS_SCREENSHOTS=1 to regenerate website documentation screenshots.');
test.describe.configure({ mode: 'serial', retries: 0 });

test('generate work management documentation screenshots', async ({ page, request }) => {
  test.setTimeout(180_000);
  mkdirSync(outputDir, { recursive: true });
  await page.setViewportSize({ width: 1280, height: 720 });

  const demo = await seedWorkManagement(request);

  if (demo.milestone?.id) {
    await capture(page, `/milestones/${demo.milestone.id}`, 'milestone-detail.png', 'Launch v1.0');
  }
  await capture(page, '/milestones', 'milestones-list.png', 'Track releases and deadlines');
  if (demo.iteration?.id) {
    await capture(page, `/iterations/${demo.iteration.id}`, 'iteration-detail.png', 'Sprint 23');
  }
  await capture(page, '/iterations', 'iterations-list.png', 'Manage iterations and releases');
  if (demo.team?.id) {
    await capture(page, `/teams/${demo.team.id}`, 'team-detail.png', 'Platform On-Call');
  }
  await capture(page, '/teams', 'teams-list.png', 'Manage teams, members, and on-call rotations');
});

async function seedWorkManagement(request: APIRequestContext) {
  const session = await tryApi(request, 'get', '/api/auth/me');
  const currentUserId = session?.user?.id ?? session?.id ?? null;

  const team = await tryApi(request, 'post', '/api/teams', {
    name: 'Platform On-Call',
    description: 'Platform and release coordination.',
    icon: 'PhoneCheck',
    color: '#2563eb',
    avatar_url: '',
  });
  if (team?.id && currentUserId) {
    await tryApi(request, 'post', `/api/teams/${team.id}/members`, {
      user_ids: [currentUserId],
      role: 'admin',
    });
  }

  const workspace = await tryApi(request, 'post', '/api/v2/workspaces', {
    name: 'Website Launch',
    key: 'WEB',
    description: 'Marketing website launch and release readiness.',
    icon: 'rocket',
    color: '#2563eb',
  });

  const category = await tryApi(request, 'post', '/api/milestone-categories', {
    name: 'Releases',
    description: 'Release milestones',
    color: '#2563eb',
  });

  const milestone = await tryApi(request, 'post', '/api/v2/milestones', {
    name: 'Launch v1.0',
    description: 'Public launch of the new marketing website.',
    target_date: daysFromNow(28),
    status: 'in-progress',
    category_id: category?.id ?? null,
  });

  const types = asArray(await tryApi(request, 'get', '/api/iteration-types'));
  const sprintType = types.find((type: any) => type.name === 'Sprint') || types[0];
  const iteration = await tryApi(request, 'post', '/api/v2/iterations', {
    name: 'Sprint 23',
    description: 'Launch readiness sprint.',
    start_date: daysFromNow(-3),
    end_date: daysFromNow(11),
    status: 'active',
    type_id: sprintType?.id ?? null,
  });

  if (workspace?.id) {
    const statuses = asArray(
      await tryApi(request, 'get', `/api/v2/workspaces/${workspace.id}/statuses`)
    );
    const statusID = statuses[0]?.id ?? null;
    const items = [
      'Review pricing page copy',
      'Publish updated user documentation',
      'Fix mobile navigation bug',
      'Draft launch announcement',
    ];
    for (const title of items) {
      await tryApi(request, 'post', '/api/v2/items', {
        workspace_id: workspace.id,
        title,
        ...(statusID ? { status_id: statusID } : {}),
        ...(iteration?.id ? { iteration_id: iteration.id } : {}),
        ...(milestone?.id ? { milestone_ids: [milestone.id] } : {}),
      });
    }
  }

  return { workspace, milestone, iteration, team };
}

async function capture(page: Page, path: string, fileName: string, visibleText: string) {
  await page.goto(path);
  await page
    .addStyleTag({
      content: `
      *, *::before, *::after {
        animation-duration: 0s !important;
        animation-delay: 0s !important;
        transition-duration: 0s !important;
        transition-delay: 0s !important;
      }
    `,
    })
    .catch(() => {});
  await page.waitForLoadState('domcontentloaded');
  await page.waitForLoadState('networkidle', { timeout: 2000 }).catch(() => {});
  await expect(page.getByText(visibleText).first()).toBeVisible({ timeout: 10000 });
  await page.screenshot({ path: `${outputDir}/${fileName}`, fullPage: false, animations: 'disabled' });
}

async function tryApi(
  request: APIRequestContext,
  method: 'get' | 'post' | 'put' | 'patch' | 'delete',
  path: string,
  data?: Record<string, unknown>
) {
  try {
    const requestHeaders =
      method === 'patch' ? { ...headers, 'Content-Type': 'application/merge-patch+json' } : headers;
    const response = await request[method](path, { headers: requestHeaders, data });
    if (!response.ok()) {
      console.warn(`[work-mgmt-docs-screenshots] ${method.toUpperCase()} ${path} -> ${response.status()}: ${await response.text()}`);
      return null;
    }
    const body = await response.json();
    return body.data ?? body;
  } catch (err) {
    console.warn(`[work-mgmt-docs-screenshots] ${method.toUpperCase()} ${path} threw:`, err);
    return null;
  }
}

function asArray(value: any): any[] {
  if (Array.isArray(value)) return value;
  if (Array.isArray(value?.data)) return value.data;
  if (Array.isArray(value?.items)) return value.items;
  return [];
}

function daysFromNow(days: number): string {
  const d = new Date();
  d.setUTCDate(d.getUTCDate() + days);
  return d.toISOString().slice(0, 10);
}
