import { expect, test } from '../fixtures/mail';
import { mkdirSync } from 'node:fs';
import { resolve } from 'node:path';
import { authenticateAdminRequest, createWorkspaceViaAPI } from '../fixtures/api-helpers';

/**
 * Playbook screenshots for the "Migrate from Jira" page.
 *
 * The Jira import API is mocked (no live Jira instance), so the wizard can be
 * driven through the project selection and mapping steps. The imported
 * workspace and work items are seeded through the normal API so the
 * user-facing captures show real Windshift surfaces.
 *
 * Run from windshift-website:
 *   PLAYBOOK_SCREENSHOTS=1 ../../windshift-core/core-tests/run-e2e.sh \
 *     tests/playbook-jira-screenshots.spec.ts --workers=1
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

const projects = [
  { id: '10001', key: 'ENG', name: 'Engineering Platform', is_team_managed: false },
  { id: '10002', key: 'SUP', name: 'Customer Support', is_team_managed: false },
  { id: '10003', key: 'OPS', name: 'Operations', is_team_managed: false },
  { id: '10004', key: 'WEB', name: 'Website Delivery', is_team_managed: false },
];

const analyze = {
  xray: { detection_status: 'not_detected', total_tests: 0 },
  projects: [
    {
      key: 'ENG',
      name: 'Engineering Platform',
      issue_count: 1842,
      suggested_workspace_key: 'ENG',
      workspace_key_collision: false,
      is_team_managed: false,
    },
    {
      key: 'SUP',
      name: 'Customer Support',
      issue_count: 624,
      suggested_workspace_key: 'SUP',
      workspace_key_collision: false,
      is_team_managed: false,
    },
  ],
  issue_types: [
    { id: '10001', name: 'Story', subtask: false, hierarchy_level: 0 },
    { id: '10002', name: 'Bug', subtask: false, hierarchy_level: 0 },
    { id: '10003', name: 'Task', subtask: false, hierarchy_level: 0 },
    { id: '10004', name: 'Sub-task', subtask: true, hierarchy_level: -1 },
  ],
  statuses: [
    { id: '1', name: 'To Do', category_key: 'new', category_name: 'To Do', color: '#dfe1e6' },
    {
      id: '2',
      name: 'In Progress',
      category_key: 'indeterminate',
      category_name: 'In Progress',
      color: '#0052cc',
    },
    {
      id: '3',
      name: 'In Review',
      category_key: 'indeterminate',
      category_name: 'In Progress',
      color: '#ff991f',
    },
    { id: '4', name: 'Done', category_key: 'done', category_name: 'Done', color: '#00875a' },
  ],
  versions: [],
  custom_fields: [
    {
      jira_field_id: 'customfield_10001',
      jira_field_name: 'Story Points',
      jira_field_type: 'number',
      windshift_field_type: 'number',
      can_map: true,
      preserve_raw: true,
    },
    {
      jira_field_id: 'customfield_10002',
      jira_field_name: 'Epic Link',
      jira_field_type: 'string',
      windshift_field_type: 'text',
      can_map: true,
      preserve_raw: true,
    },
    {
      jira_field_id: 'customfield_10003',
      jira_field_name: 'Sprint',
      jira_field_type: 'array',
      windshift_field_type: 'text',
      can_map: false,
      notes: 'No matching Windshift field type',
      preserve_raw: true,
    },
  ],
};

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

test('capture the Jira migration surfaces', async ({ request, page }) => {
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

  // Mock the Jira import API so the wizard can be driven without a live Jira.
  await page.route('**/api/admin/jira-import/connections', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([
        {
          id: 'jira-demo-connection',
          instance_url: 'https://acme.atlassian.net',
          email: 'migration@acme.example',
          instance_name: 'Acme Jira',
          deployment_type: 'cloud',
          last_used_at: '2026-07-18T09:30:00Z',
        },
      ]),
    })
  );
  await page.route('**/api/admin/jira-import/jobs', (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: '[]' })
  );
  await page.route('**/api/admin/jira-import/projects?*', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(projects),
    })
  );
  await page.route('**/api/admin/jira-import/projects/counts', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ ENG: 1842, SUP: 624, OPS: 311, WEB: 208 }),
    })
  );
  await page.route('**/api/admin/jira-import/analyze', (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(analyze) })
  );

  await page.goto(`${BASE_URL}/admin/system-import`);
  await page.getByRole('button', { name: /start import/i }).click();
  await page.getByRole('button', { name: /^continue/i }).click();
  await expect(page.getByPlaceholder(/search projects/i)).toBeVisible({ timeout: 10000 });
  await page.getByTestId('jira-import-project-ENG').click();
  await page.getByTestId('jira-import-project-SUP').click();
  await expect(page.getByText(/2 of 4 selected/i)).toBeVisible({ timeout: 5000 });
  await settle(page);
  await page.getByTestId('jira-import-wizard').screenshot({
    path: `${outputDir}/jira-import-projects.png`,
    animations: 'disabled',
  });

  // Mapping step: workspace, issue type, status, and custom field mapping.
  // A shorter viewport keeps the modal in a landscape ratio instead of the
  // near-square shape the full-height dialog produces.
  await page.getByRole('button', { name: /analyze/i }).click();
  await expect(page.getByTestId('jira-import-step-mapping')).toBeVisible({ timeout: 10000 });
  await page.setViewportSize({ width: 1440, height: 660 });
  await settle(page);
  await page.getByTestId('jira-import-wizard').screenshot({
    path: `${outputDir}/jira-import-mapping.png`,
    animations: 'disabled',
  });
  await page.setViewportSize({ width: 1440, height: 900 });

  // User-facing imported workspace and work item.
  const workspace = await createWorkspaceViaAPI(request, {
    name: 'Engineering Platform',
    key: 'ENG',
    description: 'Imported from Jira Cloud. Projects, issues, and history preserved.',
  });
  const statuses = await api(request, 'get', `/api/v2/workspaces/${workspace.id}/statuses`);
  const statusList = Array.isArray(statuses) ? statuses : (statuses.data ?? []);
  const open = statusList[0];
  const inProgress = statusList[1] ?? open;
  const done = statusList[statusList.length - 1] ?? inProgress;

  const items = [
    { title: 'Checkout service returns 502 under load', status: inProgress },
    { title: 'Retire the legacy session store', status: open },
    { title: 'Add rate limits to the public API', status: open },
    { title: 'Audit access for the payments workspace', status: inProgress },
    { title: 'Document the release rollback steps', status: done },
    { title: 'Replace the deprecated image pipeline', status: open },
  ];
  let primary = null;
  for (const item of items) {
    const created = await api(request, 'post', '/api/v2/items', {
      workspace_id: workspace.id,
      title: item.title,
      description: 'Imported from Jira with its original description and history.',
      status_id: item.status?.id,
      assignee_id: currentUser?.id,
    });
    if (!primary) primary = created;
  }

  await api(request, 'post', `/api/v2/items/${primary.id}/comments`, {
    content:
      'Imported from Jira: reported by the platform team on 2025-11-03. Original issue key ENG-1842.',
  });
  await api(request, 'post', `/api/v2/items/${primary.id}/comments`, {
    content: 'Rollback plan attached in the original Jira issue. Migration preserved the thread.',
  });

  await page.goto(`${BASE_URL}/workspaces/${workspace.id}/backlog`);
  await settle(page);
  await page.screenshot({
    path: `${outputDir}/jira-imported-backlog.png`,
    fullPage: false,
    animations: 'disabled',
  });

  await page.goto(`${BASE_URL}/workspaces/${workspace.id}/items/${primary.id}`);
  await settle(page);
  await page.screenshot({
    path: `${outputDir}/jira-imported-item.png`,
    fullPage: false,
    animations: 'disabled',
  });
});
