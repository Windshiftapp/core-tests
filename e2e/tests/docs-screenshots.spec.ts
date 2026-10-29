import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { resolve } from 'node:path';
import {
  attachRequestTypesToSection,
  createPortalChannel,
  createSimpleRequestType,
} from '../helpers/portal-setup';

const enabled = process.env.DOCS_SCREENSHOTS === '1';
const outputDir = resolve(
  process.env.DOCS_SCREENSHOTS_DIR || '../../../windshift-website/public/assets/docs'
);
const headers = { 'Sec-Fetch-Site': 'same-origin' };

test.skip(!enabled, 'Set DOCS_SCREENSHOTS=1 to regenerate website documentation screenshots.');
test.describe.configure({ mode: 'serial', retries: 0 });

test('generate website documentation screenshots with curated demo data', async ({
  page,
  request,
}) => {
  mkdirSync(outputDir, { recursive: true });
  await page.setViewportSize({ width: 1280, height: 720 });

  const session = await api(request, 'get', '/api/auth/me');
  const currentUserId = session?.user?.id ?? session?.id ?? null;
  const demo = await seedDemoData(request, currentUserId);

  await capture(page, '/workspaces', 'workspace-list.png', 'Workspaces');
  await capture(page, `/workspaces/${demo.workspace.id}/backlog`, 'backlog-item.png', 'Backlog');
  await capture(page, `/workspaces/${demo.workspace.id}/items/${demo.items.pricing.id}`, 'work-item-detail.png', 'Review pricing page copy');
  await capture(page, `/workspaces/${demo.workspace.id}/board`, 'board-cards.png', 'Website Launch');
  await capture(page, `/workspaces/${demo.workspace.id}/list`, 'list-view.png', 'Review pricing page copy');
  await capture(page, `/workspaces/${demo.workspace.id}/tree`, 'tree-view.png', 'Prepare launch checklist');
  await capture(page, `/workspaces/${demo.workspace.id}/map`, 'map-view.png', 'Review pricing page copy');
  await capture(page, `/workspaces/${demo.workspace.id}/roadmap`, 'roadmap-view.png', 'Roadmap');
  await capture(page, `/workspaces/${demo.workspace.id}/calendar`, 'calendar-view.png', 'Weekly Calendar');
  await capture(page, '/personal', 'personal-view.png', 'Personal task management');
  await capture(page, '/collections', 'collections-list.png', 'All Global Collections');
  await capture(page, '/time/worklogs', 'time-worklogs.png', 'Time Tracking');
  await capture(page, `/workspaces/${demo.workspace.id}/tests`, 'test-cases.png', 'Checkout accepts coupon codes');

  await seedPortals(request);
  await capture(page, '/channels', 'portal-hub.png', 'Customer Help Center');
});

async function seedDemoData(request: APIRequestContext, currentUserId: number | null) {
  const workspace = await api(request, 'post', '/api/v2/workspaces', {
    name: 'Website Launch',
    key: 'WEB',
    description: 'Marketing website launch, customer portal updates, and release readiness.',
    icon: 'rocket',
    color: '#2563eb',
    default_view: 'board',
  });

  const statuses = await api(request, 'get', `/api/v2/workspaces/${workspace.id}/statuses`);
  const statusByName = new Map((asArray(statuses)).map((s: any) => [String(s.name).toLowerCase(), s]));
  const open = statusByName.get('open') || asArray(statuses)[0];
  const inProgress = statusByName.get('in progress') || statusByName.get('in-progress') || asArray(statuses)[1] || open;
  const done = statusByName.get('done') || asArray(statuses)[2] || inProgress;

  const launchChecklist = await createItem(request, workspace.id, {
    title: 'Prepare launch checklist',
    description: 'Collect the final tasks required before the website launch can go live.',
    status_id: open.id,
    priority_id: null,
    assignee_id: currentUserId,
  });
  const pricing = await createItem(request, workspace.id, {
    title: 'Review pricing page copy',
    description: 'Check the pricing page for clarity, consistency, and launch readiness.',
    status_id: inProgress.id,
    priority_id: null,
    assignee_id: currentUserId,
    due_date: dateTimeFromNow(2),
    start_date: dateTimeFromNow(-1),
    end_date: dateTimeFromNow(5),
  });
  const docs = await createItem(request, workspace.id, {
    title: 'Publish updated user documentation',
    description: 'Add screenshots and user-facing guidance for the first launch cohort.',
    status_id: inProgress.id,
    priority_id: null,
    assignee_id: currentUserId,
    due_date: dateTimeFromNow(4),
    start_date: dateTimeFromNow(1),
    end_date: dateTimeFromNow(10),
  });
  const mobileBug = await createItem(request, workspace.id, {
    title: 'Fix mobile navigation bug',
    description: 'Resolve the menu overlap on small screens before release.',
    status_id: open.id,
    priority_id: null,
    due_date: dateTimeFromNow(3),
    start_date: dateTimeFromNow(6),
    end_date: dateTimeFromNow(13),
  });
  const analytics = await createItem(request, workspace.id, {
    title: 'Set up analytics dashboard',
    description: 'Create a launch dashboard for traffic, conversion, and portal requests.',
    status_id: done.id,
    priority_id: null,
  });
  const announcement = await createItem(request, workspace.id, {
    title: 'Draft launch announcement',
    description: 'Prepare the announcement post and social snippets for release day.',
    status_id: open.id,
    priority_id: null,
    parent_id: launchChecklist.id,
  });

  await seedItemLinks(request, {
    pricing: pricing.id,
    docs: docs.id,
    mobileBug: mobileBug.id,
    announcement: announcement.id,
  });

  await seedIterationAndMilestone(request, workspace.id, [pricing.id, docs.id, mobileBug.id]);

  if (currentUserId != null) {
    await seedCalendarSchedule(request, currentUserId, workspace.id, [
      { itemId: pricing.id, dayOffset: 0, time: '10:00', duration: 90 },
      { itemId: docs.id, dayOffset: 1, time: '14:00', duration: 60 },
      { itemId: mobileBug.id, dayOffset: 2, time: '09:00', duration: 45 },
    ]);
  }

  await seedTimeTracking(request, pricing.id);
  await seedTestManagement(request, workspace.id);
  await seedCollections(request, workspace.key);

  return {
    workspace,
    items: {
      launchChecklist,
      pricing,
      docs,
    },
  };
}

async function seedItemLinks(
  request: APIRequestContext,
  ids: { pricing: number; docs: number; mobileBug: number; announcement: number }
) {
  const linkTypes = asArray(await api(request, 'get', '/api/v2/link-types'));
  if (linkTypes.length === 0) return;

  const blocks =
    linkTypes.find((t: any) => /block/i.test(t.name) && !/blocked/i.test(t.name)) ||
    linkTypes[0];
  const relates =
    linkTypes.find((t: any) => /relate/i.test(t.name)) || linkTypes[0];

  await tryApi(request, 'post', '/api/v2/links', {
    link_type_id: blocks.id,
    source_type: 'item',
    source_id: ids.mobileBug,
    target_type: 'item',
    target_id: ids.announcement,
  });
  await tryApi(request, 'post', '/api/v2/links', {
    link_type_id: relates.id,
    source_type: 'item',
    source_id: ids.pricing,
    target_type: 'item',
    target_id: ids.docs,
  });
}

async function seedIterationAndMilestone(
  request: APIRequestContext,
  workspaceId: number,
  itemIds: number[]
) {
  const iterationTypes = asArray(await api(request, 'get', '/api/iteration-types'));
  const milestoneCategories = asArray(await api(request, 'get', '/api/milestone-categories'));

  let iterationId: number | null = null;
  if (iterationTypes.length > 0) {
    const iteration = await api(request, 'post', '/api/v2/iterations', {
      name: 'Sprint 23',
      description: 'Launch readiness sprint.',
      type_id: iterationTypes[0].id,
      workspace_id: workspaceId,
      status: 'active',
      start_date: daysFromNow(-3),
      end_date: daysFromNow(11),
    });
    iterationId = iteration.id;
  }

  const milestone = await api(request, 'post', '/api/v2/milestones', {
    name: 'Launch v1.0',
    description: 'Public launch of the new marketing website.',
    workspace_id: workspaceId,
    status: 'in-progress',
    target_date: daysFromNow(28),
    category_id: milestoneCategories[0]?.id ?? null,
  });

  for (const id of itemIds) {
    await tryApi(request, 'patch', `/api/v2/items/${id}`, {
      milestone_id: milestone.id,
      ...(iterationId ? { iteration_id: iterationId } : {}),
    });
  }
}

async function seedCalendarSchedule(
  request: APIRequestContext,
  userId: number,
  workspaceId: number,
  schedules: Array<{ itemId: number; dayOffset: number; time: string; duration: number }>
) {
  for (const s of schedules) {
    await tryApi(request, 'post', `/api/items/${s.itemId}/schedule`, {
      user_id: userId,
      workspace_id: workspaceId,
      scheduled_date: daysFromNow(s.dayOffset),
      scheduled_time: s.time,
      duration_minutes: s.duration,
    });
  }
}

async function seedCollections(request: APIRequestContext, workspaceKey: string) {
  await tryApi(request, 'post', '/api/v2/collections', {
    name: 'Active work in progress',
    description: 'Everything currently being worked on across every workspace.',
    ql_query: `workspaceKey = "${workspaceKey}" AND status_category = "in_progress"`,
    workspace_id: null,
    is_public: true,
  });
  await tryApi(request, 'post', '/api/v2/collections', {
    name: 'Launch blockers',
    description: 'Items linked to the launch milestone that are not yet done.',
    ql_query: `workspaceKey = "${workspaceKey}" AND milestone.name = "Launch v1.0" AND status_category != "done"`,
    workspace_id: null,
    is_public: true,
  });
}

async function seedTimeTracking(request: APIRequestContext, itemId: number) {
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

  await api(request, 'post', '/api/v2/time/worklogs', {
    project_id: project.id,
    item_id: itemId,
    description: 'Reviewed pricing copy and updated launch notes',
    date: '2026-06-01',
    duration: '1h 30m',
  });
  await api(request, 'post', '/api/v2/time/worklogs', {
    project_id: project.id,
    item_id: itemId,
    description: 'Prepared screenshots for documentation',
    date: '2026-06-01',
    duration: '45m',
  });
}

async function seedTestManagement(request: APIRequestContext, workspaceId: number) {
  await api(request, 'post', `/api/v2/workspaces/${workspaceId}/test-cases`, {
    title: 'Checkout accepts coupon codes',
    preconditions: 'A customer is signed in and has an item in the cart.',
    priority: 'high',
    status: 'active',
    estimated_duration: 10,
  });
  await api(request, 'post', `/api/v2/workspaces/${workspaceId}/test-cases`, {
    title: 'Customer can submit a portal request',
    preconditions: 'The customer portal is enabled and published.',
    priority: 'medium',
    status: 'active',
    estimated_duration: 15,
  });
}

async function seedPortals(request: APIRequestContext) {
  const helpCenter = await createPortalChannel(request, {
    slug: 'customer-help-center',
    name: 'Customer Help Center',
    description: 'Submit support requests, product feedback, and launch questions.',
    gradient: 'blue',
  });
  const partnerPortal = await createPortalChannel(request, {
    slug: 'partner-requests',
    name: 'Partner Requests',
    description: 'Requests from implementation partners and customer success teams.',
    gradient: 'sunset',
  });

  const support = await createSimpleRequestType(request, helpCenter, {
    name: 'Get support',
    description: 'Ask the team for help with setup, access, or product behavior.',
  });
  const feedback = await createSimpleRequestType(request, helpCenter, {
    name: 'Share feedback',
    description: 'Send product feedback or suggest an improvement.',
  });
  await attachRequestTypesToSection(request, helpCenter, [support.id, feedback.id]);

  const partnerRequest = await createSimpleRequestType(request, partnerPortal, {
    name: 'Request partner assistance',
    description: 'Coordinate implementation or migration work with a partner.',
  });
  await attachRequestTypesToSection(request, partnerPortal, [partnerRequest.id]);
}

async function createItem(request: APIRequestContext, workspaceId: number, data: Record<string, unknown>) {
  return api(request, 'post', '/api/v2/items', {
    workspace_id: workspaceId,
    ...data,
  });
}

async function capture(page: Page, path: string, fileName: string, visibleText: string) {
  await page.goto(path);
  await page.addStyleTag({
    content: `
      *, *::before, *::after {
        animation-duration: 0s !important;
        animation-delay: 0s !important;
        transition-duration: 0s !important;
        transition-delay: 0s !important;
      }
    `,
  }).catch(() => {});
  await page.waitForLoadState('domcontentloaded');
  await page.waitForLoadState('networkidle', { timeout: 5000 }).catch(() => {});
  await expect(page.getByText(visibleText).first()).toBeVisible({ timeout: 10000 });
  await page.screenshot({ path: `${outputDir}/${fileName}`, fullPage: false, animations: 'disabled' });
}

async function api(
  request: APIRequestContext,
  method: 'get' | 'post' | 'put' | 'patch' | 'delete',
  path: string,
  data?: Record<string, unknown>
) {
  const requestHeaders = method === 'patch'
    ? { ...headers, 'Content-Type': 'application/merge-patch+json' }
    : headers;
  const response = await request[method](path, { headers: requestHeaders, data });
  expect(response.ok(), `${method.toUpperCase()} ${path} failed: ${response.status()} ${await response.text()}`).toBeTruthy();
  const body = await response.json();
  return body.data ?? body;
}

async function tryApi(
  request: APIRequestContext,
  method: 'get' | 'post' | 'put' | 'patch' | 'delete',
  path: string,
  data?: Record<string, unknown>
) {
  try {
    const requestHeaders = method === 'patch'
      ? { ...headers, 'Content-Type': 'application/merge-patch+json' }
      : headers;
    const response = await request[method](path, { headers: requestHeaders, data });
    if (!response.ok()) {
      console.warn(`[docs-screenshots] ${method.toUpperCase()} ${path} -> ${response.status()}: ${await response.text()}`);
      return null;
    }
    const body = await response.json();
    return body.data ?? body;
  } catch (err) {
    console.warn(`[docs-screenshots] ${method.toUpperCase()} ${path} threw:`, err);
    return null;
  }
}

function asArray(value: any): any[] {
  if (Array.isArray(value)) return value;
  if (Array.isArray(value?.data)) return value.data;
  if (Array.isArray(value?.items)) return value.items;
  if (Array.isArray(value?.statuses)) return value.statuses;
  return [];
}

function daysFromNow(days: number): string {
  const d = new Date();
  d.setUTCDate(d.getUTCDate() + days);
  return d.toISOString().slice(0, 10);
}

function dateTimeFromNow(days: number): string {
  const d = new Date();
  d.setUTCDate(d.getUTCDate() + days);
  d.setUTCHours(12, 0, 0, 0);
  return d.toISOString();
}
