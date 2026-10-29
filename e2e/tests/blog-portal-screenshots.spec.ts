import { type APIRequestContext, type Page } from '@playwright/test';
import { expect, test } from '../fixtures/mail';
import { mkdirSync } from 'node:fs';
import { resolve } from 'node:path';
import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { generateWorkspace } from '../fixtures/test-data';

const enabled = process.env.BLOG_SCREENSHOTS === '1';
const outputDir = resolve(
  process.env.BLOG_SCREENSHOTS_DIR || '../../../windshift-website/public/assets/blog'
);
const headers = { 'Sec-Fetch-Site': 'same-origin' };
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

test.skip(!enabled, 'Set BLOG_SCREENSHOTS=1 to generate blog screenshots.');
test.describe.configure({ mode: 'serial', retries: 0 });

test('generate customer portal blog screenshots', async ({ page, request }) => {
  mkdirSync(outputDir, { recursive: true });
  await page.setViewportSize({ width: 1440, height: 900 });

  const stamp = Date.now();
  const workspace = await createWorkspaceViaAPI(request, {
    ...generateWorkspace(`portal-blog-${stamp}`),
    name: 'Acme Customer Delivery',
    key: `AC${String(stamp).slice(-4)}`,
    description: 'Customer requests, delivery work, milestones, tests, and follow-up in one workspace.',
  });

  const channel = await createPortalChannel(request, workspace.id, {
    slug: `acme-portal-${stamp}`,
    name: 'Acme Customer Portal',
    description: 'A customer portal connected directly to the delivery workspace.',
  });

  const onboarding = await createRequestType(request, channel.id, workspace.id, {
    name: 'New customer onboarding',
    description: 'Collect company details, environments, access, and approvals in one guided request.',
  });
  const incident = await createRequestType(request, channel.id, workspace.id, {
    name: 'Report a production issue',
    description: 'Tell the team what is affected and how urgent it is.',
  });
  const feature = await createRequestType(request, channel.id, workspace.id, {
    name: 'Suggest a product change',
    description: 'Share product feedback or request a change for a future release.',
  });

  await setRequestTypeFields(request, channel.id, onboarding.id, [
    {
      field_identifier: 'title',
      field_type: 'default',
      display_order: 0,
      is_required: true,
      step_number: 1,
      display_name: 'Company and request summary',
      description: 'Start with the customer and the outcome you need.',
    },
    {
      field_identifier: 'environment',
      field_type: 'virtual',
      display_order: 1,
      is_required: true,
      step_number: 2,
      display_name: 'Environment',
      description: 'Choose the environment this onboarding request applies to.',
      virtual_field_type: 'select',
      virtual_field_options: JSON.stringify([
        { value: 'production', label: 'Production' },
        { value: 'staging', label: 'Staging' },
        { value: 'development', label: 'Development' },
      ]),
    },
    {
      field_identifier: 'description',
      field_type: 'default',
      display_order: 2,
      is_required: false,
      step_number: 3,
      display_name: 'Access notes and approvals',
      description: 'Add users, approval notes, and anything the delivery team needs before starting.',
    },
  ]);

  await setRequestTypeFields(request, channel.id, incident.id, [
    { field_identifier: 'title', field_type: 'default', display_order: 0, is_required: true, step_number: 1 },
    { field_identifier: 'description', field_type: 'default', display_order: 1, is_required: true, step_number: 1 },
  ]);
  await setRequestTypeFields(request, channel.id, feature.id, [
    { field_identifier: 'title', field_type: 'default', display_order: 0, is_required: true, step_number: 1 },
    { field_identifier: 'description', field_type: 'default', display_order: 1, is_required: false, step_number: 1 },
  ]);

  await attachRequestTypesToSection(request, channel.id, [onboarding.id, incident.id, feature.id]);

  await page.goto(`/admin/channels/${channel.id}/portal`);
  await disableAnimations(page);
  await expect(page.getByRole('heading', { name: 'Acme Customer Portal' })).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText('Target Workspaces').first()).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText(workspace.key).first()).toBeVisible({ timeout: 10_000 });
  await page.screenshot({
    path: `${outputDir}/portal-workspace-config.png`,
    fullPage: false,
    animations: 'disabled',
  });

  await page.goto(`${BASE_URL}/portal/${channel.slug}`);
  await disableAnimations(page);
  await expect(page.getByRole('button', { name: onboarding.name })).toBeVisible({ timeout: 10_000 });
  await page.getByRole('button', { name: onboarding.name }).click();
  await expect(page.locator('#request-title')).toBeVisible({ timeout: 10_000 });
  await page.locator('#request-title').fill('Onboard Acme Finance workspace');
  await page.screenshot({
    path: `${outputDir}/portal-multistep-request.png`,
    fullPage: false,
    animations: 'disabled',
  });
});

async function createPortalChannel(
  request: APIRequestContext,
  workspaceId: number,
  opts: { slug: string; name: string; description: string }
) {
  const config = JSON.stringify({
    portal_slug: opts.slug,
    portal_workspace_ids: [workspaceId],
    portal_title: opts.name,
    portal_description: opts.description,
    portal_registration_mode: 'open',
  });
  const resp = await request.post('/api/channels', {
    headers,
    data: {
      name: opts.name,
      description: opts.description,
      type: 'portal',
      direction: 'inbound',
      status: 'enabled',
      config,
    },
  });
  expect(resp.ok(), `create portal channel: ${resp.status()} ${await resp.text()}`).toBeTruthy();
  return { ...(await resp.json()), slug: opts.slug };
}

async function createRequestType(
  request: APIRequestContext,
  channelId: number,
  workspaceId: number,
  opts: { name: string; description: string }
) {
  const resp = await request.post(`/api/channels/${channelId}/request-types`, {
    headers,
    data: {
      name: opts.name,
      description: opts.description,
      item_type_id: 1,
      is_active: true,
      icon: 'FileText',
      color: '#2563eb',
      workspace_id: workspaceId,
    },
  });
  expect(resp.ok(), `create request type: ${resp.status()} ${await resp.text()}`).toBeTruthy();
  return resp.json();
}

async function setRequestTypeFields(
  request: APIRequestContext,
  channelId: number,
  requestTypeId: number,
  fields: Array<Record<string, unknown>>
) {
  const resp = await request.put(`/api/channels/${channelId}/request-types/${requestTypeId}/fields`, {
    headers,
    data: fields,
  });
  expect(resp.ok(), `set request type fields: ${resp.status()} ${await resp.text()}`).toBeTruthy();
}

async function attachRequestTypesToSection(
  request: APIRequestContext,
  channelId: number,
  requestTypeIds: number[]
) {
  const resp = await request.put(`/api/channels/${channelId}/config`, {
    headers,
    data: {
      config: {
        portal_sections: [
          {
            id: `blog-section-${channelId}`,
            title: 'How can we help?',
            subtitle: 'Requests land in the Acme Customer Delivery workspace.',
            display_order: 0,
            request_type_ids: requestTypeIds,
            asset_report_ids: [],
          },
        ],
      },
    },
  });
  expect(resp.ok(), `attach request types: ${resp.status()} ${await resp.text()}`).toBeTruthy();
}

async function disableAnimations(page: Page) {
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
  await page.waitForLoadState('networkidle', { timeout: 5000 }).catch(() => {});
}
