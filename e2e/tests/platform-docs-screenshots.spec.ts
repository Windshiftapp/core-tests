import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { resolve } from 'node:path';

// Platform-feature captures (actions, analytics, assets, knowledge base) for
// the website documentation. Seeds its own workspace, asset set, and bucket.
const enabled = process.env.DOCS_SCREENSHOTS === '1';
const outputDir = resolve(
  process.env.DOCS_SCREENSHOTS_DIR || '../../../windshift-website/public/assets/docs'
);
const headers = { 'Sec-Fetch-Site': 'same-origin' };

test.skip(!enabled, 'Set DOCS_SCREENSHOTS=1 to regenerate website documentation screenshots.');
test.describe.configure({ mode: 'serial', retries: 0 });

test('generate platform documentation screenshots', async ({ page, request }) => {
  test.setTimeout(180_000);
  mkdirSync(outputDir, { recursive: true });
  await page.setViewportSize({ width: 1280, height: 720 });

  const demo = await seedWorkspaceAndItems(request);
  await seedAssetSet(request);

  if (demo.workspace?.id) {
    await capture(page, `/workspaces/${demo.workspace.id}/actions`, 'actions.png', 'Actions');
    await capture(
      page,
      `/workspaces/${demo.workspace.id}/analytics`,
      'analytics.png',
      'Delivery health and flow'
    );
  }
  await capture(page, '/assets/settings', 'assets.png', 'Asset Management');
});

async function seedWorkspaceAndItems(request: APIRequestContext) {
  const workspace = await tryApi(request, 'post', '/api/v2/workspaces', {
    name: 'Website Launch',
    key: 'WEB',
    description: 'Marketing website launch and release readiness.',
    icon: 'rocket',
    color: '#2563eb',
  });
  if (!workspace?.id) return { workspace };

  const statuses = asArray(
    await tryApi(request, 'get', `/api/v2/workspaces/${workspace.id}/statuses`)
  );
  const byName = (name: string) =>
    statuses.find((s: any) => String(s.name).toLowerCase() === name.toLowerCase());
  const open = byName('open') || statuses[0];
  const done = byName('done') || statuses[statuses.length - 1];

  const titles = [
    'Review pricing page copy',
    'Publish updated user documentation',
    'Fix mobile navigation bug',
    'Draft launch announcement',
    'Prepare launch checklist',
    'Set up analytics dashboard',
  ];
  for (const title of titles) {
    await tryApi(request, 'post', '/api/v2/items', {
      workspace_id: workspace.id,
      title,
      ...(open?.id ? { status_id: open.id } : {}),
    });
  }

  // Complete two items so delivery-time and throughput charts have data.
  const items = asArray(
    await tryApi(request, 'get', `/api/v2/items?workspace_id=${workspace.id}&limit=100`)
  );
  if (done?.id) {
    for (const item of items.slice(0, 2)) {
      await tryApi(request, 'post', `/api/v2/items/${item.id}/transition`, {
        to_status_id: done.id,
      });
    }
  }
  return { workspace };
}

async function seedAssetSet(request: APIRequestContext) {
  await tryApi(request, 'post', '/api/v2/asset-sets', {
    name: 'Customer equipment',
    description: 'Devices and services tracked for customers.',
  });
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
  await page.waitForLoadState('networkidle', { timeout: 2500 }).catch(() => {});
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
      console.warn(`[platform-docs] ${method.toUpperCase()} ${path} -> ${response.status()}: ${await response.text()}`);
      return null;
    }
    const body = await response.json();
    return body.data ?? body;
  } catch (err) {
    console.warn(`[platform-docs] ${method.toUpperCase()} ${path} threw:`, err);
    return null;
  }
}

function asArray(value: any): any[] {
  if (Array.isArray(value)) return value;
  if (Array.isArray(value?.data)) return value.data;
  if (Array.isArray(value?.items)) return value.items;
  return [];
}
