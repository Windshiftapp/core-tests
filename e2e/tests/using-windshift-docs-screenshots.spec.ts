import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { resolve } from 'node:path';

// Daily-use captures (profile, security, notifications, search, command
// palette) for the website documentation.
const enabled = process.env.DOCS_SCREENSHOTS === '1';
const outputDir = resolve(
  process.env.DOCS_SCREENSHOTS_DIR || '../../../windshift-website/public/assets/docs'
);
const headers = { 'Sec-Fetch-Site': 'same-origin' };

test.skip(!enabled, 'Set DOCS_SCREENSHOTS=1 to regenerate website documentation screenshots.');
test.describe.configure({ mode: 'serial', retries: 0 });

test('generate using-windshift documentation screenshots', async ({ page, request }) => {
  test.setTimeout(180_000);
  mkdirSync(outputDir, { recursive: true });
  await page.setViewportSize({ width: 1280, height: 720 });

  await seedSearchData(request);
  await seedNotifications(request);

  await capture(page, '/profile', 'profile.png', 'Manage your profile information');
  await capture(page, '/security', 'security-account.png', 'Manage your security settings');
  await capture(page, '/notifications', 'notifications.png', 'Manage notification settings');
  await capture(page, '/search?search=launch', 'search.png', 'Search across all workspaces');
  await capturePalette(page, 'command-palette.png');
});

async function seedSearchData(request: APIRequestContext) {
  const workspace = await tryApi(request, 'post', '/api/v2/workspaces', {
    name: 'Website Launch',
    key: 'WEB',
    description: 'Marketing website launch and release readiness.',
    icon: 'rocket',
    color: '#2563eb',
  });
  if (!workspace?.id) return;
  const statuses = asArray(
    await tryApi(request, 'get', `/api/v2/workspaces/${workspace.id}/statuses`)
  );
  const statusID = statuses[0]?.id ?? null;
  for (const title of ['Draft launch announcement', 'Prepare launch checklist']) {
    await tryApi(request, 'post', '/api/v2/items', {
      workspace_id: workspace.id,
      title,
      ...(statusID ? { status_id: statusID } : {}),
    });
  }
}

async function seedNotifications(request: APIRequestContext) {
  const notifications = [
    {
      title: 'Sam Rivera mentioned you',
      message: 'Can you review the pricing page copy before we publish?',
      type: 'mention',
    },
    {
      title: 'Assigned: Review pricing page copy',
      message: 'This work item is assigned to you.',
      type: 'assignment',
    },
    {
      title: 'Status changed to In Progress',
      message: 'Prepare launch checklist moved to In Progress.',
      type: 'status_change',
    },
    {
      title: 'Launch v1.0 is due in 28 days',
      message: 'The milestone Launch v1.0 is approaching its target date.',
      type: 'milestone',
    },
  ];
  for (const notification of notifications) {
    await tryApi(request, 'post', '/api/notifications', {
      ...notification,
      action_url: '/',
    });
  }
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

async function capturePalette(page: Page, fileName: string) {
  await page.goto('/collections');
  await page.waitForLoadState('domcontentloaded');
  await page.waitForLoadState('networkidle', { timeout: 2000 }).catch(() => {});
  await page.locator('#global-search-button').click();
  const input = page.getByTestId('command-palette-input');
  await expect(input.first()).toBeVisible({ timeout: 10000 });
  await page.waitForTimeout(300);
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
      console.warn(`[using-windshift-docs] ${method.toUpperCase()} ${path} -> ${response.status()}: ${await response.text()}`);
      return null;
    }
    const body = await response.json();
    return body.data ?? body;
  } catch (err) {
    console.warn(`[using-windshift-docs] ${method.toUpperCase()} ${path} threw:`, err);
    return null;
  }
}

function asArray(value: any): any[] {
  if (Array.isArray(value)) return value;
  if (Array.isArray(value?.data)) return value.data;
  if (Array.isArray(value?.items)) return value.items;
  return [];
}
