import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { type APIRequestContext, expect, type Page } from '../fixtures/context-path';
import { generateWorkspace } from '../fixtures/test-data';

/**
 * Portal-test setup helpers. Builds the minimum scaffolding for a portal-UI
 * test: a workspace, a portal channel pointed at that workspace, and one or
 * two request types with controllable form-step layouts.
 *
 * Lower-level than fixtures/api-helpers.ts because portal channels are only
 * used by a small set of specs; promoted to a helper instead of duplicating
 * the four-request setup in each spec.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
// Origin only (no context-path prefix). The magic link already carries the
// correct path (the server builds it from BASE_URL, which includes any context
// path), so normalisation must repoint only scheme://host — using BASE_URL here
// would duplicate the context-path prefix (/windshift/windshift/portal/...).
const BASE_ORIGIN = process.env.BASE_ORIGIN || new URL(BASE_URL).origin;

export interface PortalChannelHandle {
  channelId: number;
  workspaceId: number;
  slug: string;
  name: string;
}

export interface RequestTypeHandle {
  id: number;
  name: string;
}

/**
 * Create a workspace + a portal channel that targets it. The channel is
 * registration_mode=open so any email can request a magic link.
 *
 * Uses admin storageState (the project default) via the supplied request
 * context. Don't call this from a customer-scoped (empty storageState) context.
 */
export async function createPortalChannel(
  request: APIRequestContext,
  opts: { slug: string; name: string }
): Promise<PortalChannelHandle> {
  const ws = await createWorkspaceViaAPI(request, generateWorkspace(opts.slug));

  const resp = await request.post('/api/channels', {
    headers: SEC_FETCH,
    data: {
      name: opts.name,
      type: 'portal',
      direction: 'inbound',
      status: 'disabled',
      slug: opts.slug,
    },
  });
  expect(resp.ok(), `create portal channel: ${resp.status()} ${await resp.text()}`).toBeTruthy();
  const channel = await resp.json();

  // Channel creation accepts the public slug, but channel-specific settings
  // are configured through the dedicated config endpoint. Keeping this as a
  // separate request mirrors the management UI and ensures request-type
  // routing sees the workspace served by the portal.
  const configResp = await request.put(`/api/channels/${channel.id}/config`, {
    headers: SEC_FETCH,
    data: {
      config: {
        portal_slug: opts.slug,
        portal_workspace_ids: [ws.id],
        portal_title: opts.name,
        portal_registration_mode: 'open',
      },
    },
  });
  expect(
    configResp.ok(),
    `configure portal channel: ${configResp.status()} ${await configResp.text()}`
  ).toBeTruthy();

  const toggleResp = await request.put(`/api/channels/${channel.id}/toggle`, {
    headers: SEC_FETCH,
  });
  expect(
    toggleResp.ok(),
    `enable portal channel: ${toggleResp.status()} ${await toggleResp.text()}`
  ).toBeTruthy();

  return { channelId: channel.id, workspaceId: ws.id, slug: opts.slug, name: opts.name };
}

/**
 * Add a single default section to the portal that holds the supplied
 * request types. Without this step request types are created but never
 * appear on the portal home (the FE renders request types as children of
 * portal_sections only).
 *
 * Re-PUTs the full config rather than mutating-in-place because the
 * UpdateChannelConfig endpoint replaces the whole config blob.
 */
export async function attachRequestTypesToSection(
  request: APIRequestContext,
  channel: PortalChannelHandle,
  requestTypeIds: number[]
): Promise<void> {
  // The UpdateChannelConfig handler merges incoming config keys into the
  // existing config blob, so only the section list needs to be sent —
  // slug/workspace_ids/title are preserved from the create call.
  const section = {
    id: `e2e-section-${channel.channelId}`,
    title: '',
    subtitle: '',
    display_order: 0,
    request_type_ids: requestTypeIds,
    asset_report_ids: [],
  };
  const resp = await request.put(`/api/channels/${channel.channelId}/config`, {
    headers: SEC_FETCH,
    data: { config: { portal_sections: [section] } },
  });
  expect(
    resp.ok(),
    `attach request types to section: ${resp.status()} ${await resp.text()}`
  ).toBeTruthy();
}

/**
 * Create a request type with default fields on a single step (title +
 * description, both required, step 1). Suitable for the happy-path submission
 * test where multi-step navigation isn't under exercise.
 */
export async function createSimpleRequestType(
  request: APIRequestContext,
  channelId: number,
  opts: { name: string }
): Promise<RequestTypeHandle> {
  const rt = await createRequestType(request, channelId, opts.name);
  await setRequestTypeFields(request, channelId, rt.id, [
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
  ]);
  return rt;
}

/**
 * Create a request type whose form is split across two steps: title on step
 * 1, description on step 2. Used by the draft save/resume test to verify
 * that auto-save fires on step advance and that the resumed form lands on
 * the saved current_step.
 *
 * No custom-field definitions are required — splitting the two default
 * fields is enough to produce a 2-step UI.
 */
export async function createTwoStepRequestType(
  request: APIRequestContext,
  channelId: number,
  opts: { name: string }
): Promise<RequestTypeHandle> {
  const rt = await createRequestType(request, channelId, opts.name);
  await setRequestTypeFields(request, channelId, rt.id, [
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
      step_number: 2,
    },
  ]);
  return rt;
}

async function createRequestType(
  request: APIRequestContext,
  channelId: number,
  name: string
): Promise<RequestTypeHandle> {
  const resp = await request.post(`/api/channels/${channelId}/request-types`, {
    headers: SEC_FETCH,
    data: {
      name,
      // item_type_id=1 is the seeded default item type. Same convention
      // approvals.spec.ts and portal-asset-reports-visibility.spec.ts use.
      item_type_id: 1,
      is_active: true,
    },
  });
  expect(resp.ok(), `create request type: ${resp.status()} ${await resp.text()}`).toBeTruthy();
  const rt = await resp.json();
  return { id: rt.id, name: rt.name };
}

interface RequestTypeFieldInput {
  field_identifier: string;
  field_type: 'default' | 'custom' | 'virtual';
  display_order: number;
  is_required: boolean;
  step_number: number;
}

async function setRequestTypeFields(
  request: APIRequestContext,
  channelId: number,
  requestTypeId: number,
  fields: RequestTypeFieldInput[]
): Promise<void> {
  const resp = await request.put(
    `/api/channels/${channelId}/request-types/${requestTypeId}/fields`,
    {
      headers: SEC_FETCH,
      data: fields,
    }
  );
  expect(resp.ok(), `set request type fields: ${resp.status()} ${await resp.text()}`).toBeTruthy();
}

export interface SignInOptions {
  /** Mailpit fixture exposing waitForLast / extractLink. */
  mail: {
    waitForLast: (opts: {
      to: string;
      subject?: RegExp | string;
      since?: Date;
      timeoutMs?: number;
    }) => Promise<{ Text: string }>;
  };
  slug: string;
  email: string;
}

/**
 * Drive the portal login modal end-to-end: open it (via the header), submit
 * the email, wait for the Mailpit message, extract the magic-link URL, and
 * navigate to it. Resolves once the verify page reaches a non-`verifying`
 * status — caller asserts whether success or error landed.
 *
 * Caller is responsible for ensuring the login modal is reachable (e.g.,
 * navigated to /portal/{slug} or about to be prompted by a gated action).
 */
export async function signInViaMagicLink(page: Page, opts: SignInOptions): Promise<void> {
  // The login modal opens from the header Sign-In button on the portal home,
  // or auto-opens when a request-type card is clicked while unauthenticated.
  // Tests using this helper should have it visible before calling.
  await expect(page.locator('#email')).toBeVisible({ timeout: 5000 });
  await page.locator('#email').fill(opts.email);
  const since = new Date();
  await page.locator('[data-testid="portal-login-request-magic-link"]').click();

  const msg = await opts.mail.waitForLast({
    to: opts.email,
    subject: 'Sign in to your portal',
    since,
    timeoutMs: 5000,
  });
  const linkMatch = msg.Text.match(/(https?:\/\/\S+\/portal\/\S+\/verify#token=[^\s>]+)/);
  if (!linkMatch) throw new Error(`magic-link URL not found in body: ${msg.Text.slice(0, 200)}`);
  let link = linkMatch[1];
  // Normalise origin so the test can run against any port (keep the path,
  // which already includes any context-path prefix).
  link = link.replace(/^https?:\/\/[^/]+/, BASE_ORIGIN);

  await page.goto(link);
  // PortalVerifyLink shows `verifying` → `success` (redirects) or `error`.
  // Wait for either terminal state by waiting on a network response to the
  // verify endpoint, then settle on the portal home.
  await page.waitForURL(/\/portal\/[^/]+(\?|$|#)/, { timeout: 10000 });
}
