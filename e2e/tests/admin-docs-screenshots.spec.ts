import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { resolve } from 'node:path';

// Focused admin-surface captures for the website documentation. Kept separate
// from docs-screenshots.spec.ts so admin screenshots do not depend on the
// work-management demo fixture.
const enabled = process.env.DOCS_SCREENSHOTS === '1';
const outputDir = resolve(
  process.env.DOCS_SCREENSHOTS_DIR || '../../../windshift-website/public/assets/docs'
);
const headers = { 'Sec-Fetch-Site': 'same-origin' };

test.skip(!enabled, 'Set DOCS_SCREENSHOTS=1 to regenerate website documentation screenshots.');
test.describe.configure({ mode: 'serial', retries: 0 });

test('generate admin documentation screenshots', async ({ page, request }) => {
  test.setTimeout(180_000);
  mkdirSync(outputDir, { recursive: true });
  await page.setViewportSize({ width: 1280, height: 720 });

  await seedCustomFields(request);
  await seedUsersAndGroups(request);
  await seedGovernance(request);

  // Wait on each page subtitle so the capture happens after the tab data loads.
  await capture(page, '/admin/custom-fields', 'admin-custom-fields.png', 'Define custom fields for your items');
  await capture(page, '/admin/screens', 'admin-screens.png', 'Define field layouts for issues and projects');
  await capture(
    page,
    '/admin/configuration-sets',
    'admin-configuration-sets.png',
    'Manage configuration sets that combine workflows and screens'
  );
  await capture(page, '/admin/item-types', 'admin-item-types.png', 'Manage item types and assign them to hierarchy levels');
  await capture(page, '/admin/hierarchy-levels', 'admin-hierarchy-levels.png', 'Manage the hierarchy levels in your system');
  await capture(page, '/admin/priorities', 'admin-priorities.png', 'Configure priority levels with icons and colors');
  await capture(page, '/admin/statuses', 'admin-statuses.png', 'Manage individual statuses');
  await capture(page, '/admin/workflows', 'admin-workflows.png', 'Design and manage workflow transitions between statuses');
  await capture(page, '/admin/condition-sets', 'admin-condition-sets.png', 'Define conditions that restrict workflow transitions');
  await capture(page, '/admin/approval-sets', 'admin-approval-sets.png', 'Multi-step approvals that gate transitions out of a status');
  await capture(page, '/admin/permissions', 'admin-global-permissions.png', 'Manage global permissions for users and groups');
  await capture(page, '/admin/users', 'admin-users.png', 'Manage user accounts and permissions');
  await capture(page, '/admin/groups', 'admin-groups.png', 'Manage user groups and memberships for access control');
  await capture(page, '/admin/workspace-roles', 'admin-workspace-roles.png', 'Manage roles and access levels');
  await capture(page, '/admin/security', 'admin-security.png', 'Manage security-related settings and feature controls');
  await capture(
    page,
    '/admin/notification-settings',
    'admin-notification-settings.png',
    'Create and manage notification configurations that can be assigned to configuration sets'
  );
  await capture(
    page,
    '/admin/email-templates',
    'admin-email-templates.png',
    'Customize the subject and body of transactional emails sent by Windshift'
  );
});

async function seedCustomFields(request: APIRequestContext) {
  await tryApi(request, 'post', '/api/v2/custom-fields', {
    name: 'Support tier',
    field_type: 'text',
    description: 'Contract level that applies to this request.',
    required: false,
    options: '',
    display_order: 1,
    applies_to_portal_customers: true,
    applies_to_customer_organisations: false,
  });
  await tryApi(request, 'post', '/api/v2/custom-fields', {
    name: 'Effort estimate',
    field_type: 'number',
    description: 'Planned effort in story points.',
    required: false,
    options: '',
    display_order: 2,
    applies_to_portal_customers: false,
    applies_to_customer_organisations: false,
  });
}

async function seedUsersAndGroups(request: APIRequestContext) {
  const user = await tryApi(request, 'post', '/api/users', {
    email: 'sam.rivera@example.com',
    username: 'sam.rivera',
    first_name: 'Sam',
    last_name: 'Rivera',
    password: 'TestPass123!',
    is_active: true,
  });
  const group = await tryApi(request, 'post', '/api/v2/admin/groups', {
    name: 'Platform Team',
    description: 'Platform and release coordination.',
  });
  if (group?.id && user?.id) {
    await tryApi(request, 'patch', `/api/v2/admin/groups/${group.id}`, { member_ids: [user.id] });
  }
}

async function seedGovernance(request: APIRequestContext) {
  const workflows = asArray(await tryApi(request, 'get', '/api/v2/workflows'));
  const workflow = workflows.find((w: any) => w.builtin_key === 'default') || workflows[0];
  if (!workflow) return;

  const transitions = asArray(
    await tryApi(request, 'get', `/api/v2/workflows/${workflow.id}/transitions`)
  );
  const groups = asArray(await tryApi(request, 'get', '/api/groups'));
  const platform = groups.find((g: any) => g.name === 'Platform Team');

  const openToInProgress = transitions.find(
    (t: any) => t.from?.name === 'Open' && t.to?.name === 'In Progress'
  );
  const openToDone = transitions.find((t: any) => t.from?.name === 'Open' && t.to?.name === 'Done');

  if (openToInProgress && platform) {
    await tryApi(request, 'post', '/api/v2/condition-sets', {
      name: 'Release gates',
      description: 'Only the platform team can start work on an item.',
      workflow_id: workflow.id,
      transition_conditions: [
        {
          transition_id: openToInProgress.id,
          logic_mode: 'and',
          conditions: [
            {
              condition_type: 'user_in_group',
              config: { source: 'current_user', group_id: platform.id },
              mode: 'condition',
              display_order: 1,
            },
          ],
        },
      ],
    });
  }

  if (openToInProgress && openToDone && platform) {
    await tryApi(request, 'post', '/api/v2/approval-sets', {
      name: 'Release approval',
      description: 'A platform team member must approve before work starts.',
      workflow_id: workflow.id,
      set_statuses: [
        {
          status_id: openToInProgress.from.id,
          approve_transition_id: openToInProgress.id,
          deny_transition_id: openToDone.id,
          step_mode: 'sequential',
          steps: [
            {
              name: 'Lead reviewer',
              quorum_mode: 'any',
              rejection_policy: 'any_rejection_fails',
              approver_source: 'group',
              approver_group_id: platform.id,
              allow_self_approval: false,
              on_leave_strategy: 'use_substitute',
            },
          ],
        },
      ],
    });
  }
}

function asArray(value: any): any[] {
  if (Array.isArray(value)) return value;
  if (Array.isArray(value?.data)) return value.data;
  if (Array.isArray(value?.items)) return value.items;
  return [];
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
      console.warn(`[admin-docs-screenshots] ${method.toUpperCase()} ${path} -> ${response.status()}: ${await response.text()}`);
      return null;
    }
    const body = await response.json();
    return body.data ?? body;
  } catch (err) {
    console.warn(`[admin-docs-screenshots] ${method.toUpperCase()} ${path} threw:`, err);
    return null;
  }
}
