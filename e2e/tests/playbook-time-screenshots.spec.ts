import { expect, test } from '../fixtures/mail';
import { mkdirSync } from 'node:fs';
import { resolve } from 'node:path';
import { authenticateAdminRequest, createWorkspaceViaAPI } from '../fixtures/api-helpers';

/**
 * Playbook screenshots for the "Track time next to the work" page.
 *
 * Seeds a workspace, work items, a customer, and time projects, then captures
 * the user-facing time entry and the admin time project surfaces. Writes PNGs
 * to windshift-website/public/originals for the website's optimize script.
 *
 * Run from windshift-website:
 *   PLAYBOOK_SCREENSHOTS=1 ../../windshift-core/core-tests/run-e2e.sh \
 *     tests/playbook-time-screenshots.spec.ts --workers=1
 */

const enabled = process.env.PLAYBOOK_SCREENSHOTS === '1';
const outputDir = resolve(
  process.env.PLAYBOOK_SCREENSHOTS_DIR || '../../../windshift-website/public/originals'
);
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

test.skip(!enabled, 'Set PLAYBOOK_SCREENSHOTS=1 to generate playbook screenshots.');
test.describe.configure({ mode: 'serial', retries: 0 });
test.use({ deviceScaleFactor: 2 });

async function api(request, method, path, data) {
  const resp = await request[method](`${BASE_URL}${path}`, {
    headers: SEC_FETCH,
    ...(data ? { data } : {}),
  });
  expect(resp.ok(), `${method} ${path}: ${resp.status()} ${await resp.text()}`).toBeTruthy();
  const text = await resp.text();
  if (!text) return null;
  const body = JSON.parse(text);
  return body.data ?? body;
}

function daysFromNow(days) {
  const d = new Date();
  d.setUTCDate(d.getUTCDate() + days);
  return d.toISOString().slice(0, 10);
}

async function settle(page) {
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
  await page.waitForLoadState('networkidle', { timeout: 5000 }).catch(() => {});
}

async function capture(page, path, fileName, visibleText) {
  await page.goto(`${BASE_URL}${path}`);
  await settle(page);
  await expect(page.getByText(visibleText).first()).toBeVisible({ timeout: 10000 });
  await page.screenshot({ path: `${outputDir}/${fileName}`, fullPage: false, animations: 'disabled' });
}

test('capture the time tracking surfaces', async ({ request, page }) => {
  await authenticateAdminRequest(request);
  mkdirSync(outputDir, { recursive: true });
  await page.setViewportSize({ width: 1440, height: 900 });

  const meResp = await request.get('/api/auth/me');
  const me = await meResp.json();
  const currentUser = me?.user ?? me;
  if (currentUser?.id) {
    await request.patch(`/api/v2/admin/users/${currentUser.id}`, {
      headers: { 'Content-Type': 'application/merge-patch+json', ...SEC_FETCH },
      data: {
        email: currentUser.email,
        username: currentUser.username,
        first_name: 'Alex',
        last_name: 'Rivera',
        timezone: currentUser.timezone || 'UTC',
        language: currentUser.language || 'en',
        is_active: currentUser.is_active ?? true,
      },
    });
  }

  const workspace = await createWorkspaceViaAPI(request, {
    name: 'Website Launch',
    key: 'WEB',
    description: 'Marketing site, docs, and launch readiness.',
  });

  const statuses = await api(request, 'get', `/api/v2/workspaces/${workspace.id}/statuses`);
  const statusList = Array.isArray(statuses) ? statuses : (statuses.data ?? []);
  const open = statusList[0];
  const inProgress = statusList[1] ?? open;

  const pricing = await api(request, 'post', '/api/v2/items', {
    workspace_id: workspace.id,
    title: 'Review pricing page copy',
    description: 'Check the pricing page for clarity, consistency, and launch readiness.',
    status_id: inProgress?.id,
    assignee_id: currentUser?.id,
  });
  const docs = await api(request, 'post', '/api/v2/items', {
    workspace_id: workspace.id,
    title: 'Publish updated user documentation',
    description: 'Add screenshots and user-facing guidance for the first launch cohort.',
    status_id: inProgress?.id,
    assignee_id: currentUser?.id,
  });
  const mobile = await api(request, 'post', '/api/v2/items', {
    workspace_id: workspace.id,
    title: 'Fix mobile navigation bug',
    description: 'Resolve the menu overlap on small screens before release.',
    status_id: open?.id,
    assignee_id: currentUser?.id,
  });

  const customer = await api(request, 'post', '/api/customer-organisations', {
    name: 'Acme Corp',
    description: 'Launch customer for the website rollout.',
    email: 'hello@acme.example',
    active: true,
  });
  const project = await api(request, 'post', '/api/v2/time/projects', {
    customer_id: customer.id,
    name: 'Website launch support',
    description: 'Design, documentation, and release support for the launch.',
    status: 'Active',
    color: '#2563eb',
  });
  const northwind = await api(request, 'post', '/api/customer-organisations', {
    name: 'Northwind Traders',
    description: 'Operations team and platform maintenance.',
    email: 'ops@northwind.example',
    active: true,
  });
  const internal = await api(request, 'post', '/api/v2/time/projects', {
    customer_id: northwind.id,
    name: 'Platform maintenance',
    description: 'Maintenance and platform improvements for the operations team.',
    status: 'Active',
    color: '#7c3aed',
  });

  await api(request, 'post', '/api/v2/time/worklogs', {
    project_id: project.id,
    item_id: pricing.id,
    description: 'Reviewed pricing copy and updated launch notes',
    date: daysFromNow(-2),
    duration: '1h 30m',
  });
  await api(request, 'post', '/api/v2/time/worklogs', {
    project_id: project.id,
    item_id: docs.id,
    description: 'Prepared screenshots for documentation',
    date: daysFromNow(-1),
    duration: '45m',
  });
  await api(request, 'post', '/api/v2/time/worklogs', {
    project_id: internal.id,
    item_id: mobile.id,
    description: 'Investigated the menu overlap on small screens',
    date: daysFromNow(0),
    duration: '2h',
  });

  await capture(page, '/time', 'time-entry.png', 'Time Entry');
  await capture(page, '/time/projects', 'time-projects.png', 'Projects');
  await capture(page, '/time/worklogs', 'time-worklogs.png', 'Time Tracking');

  // Work item Time Tracking tab: the timer and worklogs sit on the item.
  await page.goto(`${BASE_URL}/workspaces/${workspace.id}/items/${pricing.id}`);
  await settle(page);
  await page.getByTestId('item-detail-time-tab').click();
  await settle(page);
  await page.screenshot({
    path: `${outputDir}/time-work-item.png`,
    fullPage: false,
    animations: 'disabled',
  });
});
