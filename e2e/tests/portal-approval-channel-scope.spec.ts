import { createWorkspaceViaAPI } from '../fixtures/api-helpers';
import type { APIRequestContext } from '../fixtures/context-path';
import { expect, test } from '../fixtures/mail';
import { generateWorkspace } from '../fixtures/test-data';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };
const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';

async function createStatus(
  request: APIRequestContext,
  name: string,
  categoryID: number
): Promise<number> {
  const response = await request.post('/api/statuses', {
    headers: SEC_FETCH,
    data: { name, category_id: categoryID },
  });
  expect(response.ok(), `create status ${name}: ${await response.text()}`).toBeTruthy();
  return (await response.json()).id;
}

async function buildApprovalFixture(
  request: APIRequestContext,
  prefix: string,
  workspaceID: number
) {
  const categoriesResponse = await request.get('/api/status-categories', {
    headers: SEC_FETCH,
  });
  expect(categoriesResponse.ok()).toBeTruthy();
  const categories = (await categoriesResponse.json()) as Array<{
    id: number;
    is_default: boolean;
  }>;
  const categoryID = (categories.find((category) => category.is_default) ?? categories[0]).id;

  const reviewStatusID = await createStatus(request, `${prefix}-Review`, categoryID);
  const approvedStatusID = await createStatus(request, `${prefix}-Approved`, categoryID);
  const rejectedStatusID = await createStatus(request, `${prefix}-Rejected`, categoryID);

  const workflowResponse = await request.post('/api/workflows', {
    headers: SEC_FETCH,
    data: {
      name: `${prefix}-workflow`,
      description: 'portal channel scope e2e',
    },
  });
  expect(workflowResponse.ok(), `create workflow: ${await workflowResponse.text()}`).toBeTruthy();
  const workflowID = (await workflowResponse.json()).id;

  const transitionsResponse = await request.put(`/api/workflows/${workflowID}/transitions`, {
    headers: SEC_FETCH,
    data: [
      { from_status_id: null, to_status_id: 1 },
      { from_status_id: 1, to_status_id: reviewStatusID },
      { from_status_id: reviewStatusID, to_status_id: approvedStatusID },
      { from_status_id: reviewStatusID, to_status_id: rejectedStatusID },
    ],
  });
  expect(
    transitionsResponse.ok(),
    `create transitions: ${await transitionsResponse.text()}`
  ).toBeTruthy();
  const transitions = (await transitionsResponse.json()) as Array<{
    id: number;
    from_status_id: number | null;
    to_status_id: number;
  }>;
  const approveTransitionID = transitions.find(
    (transition) =>
      transition.from_status_id === reviewStatusID && transition.to_status_id === approvedStatusID
  )?.id;
  const denyTransitionID = transitions.find(
    (transition) =>
      transition.from_status_id === reviewStatusID && transition.to_status_id === rejectedStatusID
  )?.id;
  expect(approveTransitionID).toBeTruthy();
  expect(denyTransitionID).toBeTruthy();

  const approvalSetResponse = await request.post('/api/approval-sets', {
    headers: SEC_FETCH,
    data: {
      name: `${prefix}-approval-set`,
      workflow_id: workflowID,
      set_statuses: [
        {
          status_id: reviewStatusID,
          approve_transition_id: approveTransitionID,
          deny_transition_id: denyTransitionID,
          step_mode: 'sequential',
          steps: [
            {
              display_order: 0,
              name: 'Creator decision',
              quorum_mode: 'any',
              approver_source: 'creator',
              allow_self_approval: true,
              on_leave_strategy: 'keep',
            },
          ],
        },
      ],
    },
  });
  expect(
    approvalSetResponse.status(),
    `create approval set: ${await approvalSetResponse.text()}`
  ).toBe(201);
  const approvalSetID = (await approvalSetResponse.json()).id;

  const configurationResponse = await request.post('/api/configuration-sets', {
    headers: SEC_FETCH,
    data: {
      name: `${prefix}-configuration`,
      workflow_id: workflowID,
      approval_set_id: approvalSetID,
      workspace_ids: [workspaceID],
    },
  });
  expect(
    configurationResponse.ok(),
    `create configuration: ${await configurationResponse.text()}`
  ).toBeTruthy();

  return { reviewStatusID };
}

async function createPortal(
  request: APIRequestContext,
  slug: string,
  workspaceID: number
): Promise<void> {
  const createResponse = await request.post('/api/channels', {
    headers: SEC_FETCH,
    data: {
      name: `${slug} portal`,
      type: 'portal',
      direction: 'inbound',
      status: 'disabled',
      slug,
    },
  });
  expect(createResponse.ok(), `create portal ${slug}: ${await createResponse.text()}`).toBeTruthy();
  const channelID = (await createResponse.json()).id;

  const configResponse = await request.put(`/api/channels/${channelID}/config`, {
    headers: SEC_FETCH,
    data: {
      config: {
        portal_slug: slug,
        portal_title: `${slug} portal`,
        portal_workspace_ids: [workspaceID],
        portal_registration_mode: 'open',
      },
    },
  });
  expect(
    configResponse.ok(),
    `configure portal ${slug}: ${await configResponse.text()}`
  ).toBeTruthy();

  const toggleResponse = await request.put(`/api/channels/${channelID}/toggle`, {
    headers: SEC_FETCH,
  });
  expect(toggleResponse.ok(), `enable portal ${slug}: ${await toggleResponse.text()}`).toBeTruthy();
}

async function submitAndOpenApproval(
  actor: APIRequestContext,
  admin: APIRequestContext,
  slug: string,
  title: string,
  reviewStatusID: number
): Promise<{ itemID: number; approvalID: number }> {
  const submitResponse = await actor.post(`/api/portal/${slug}/submit`, {
    headers: SEC_FETCH,
    data: { title, description: `${title} description` },
  });
  expect(submitResponse.ok(), `submit ${title}: ${await submitResponse.text()}`).toBeTruthy();
  const submitBody = await submitResponse.json();
  const itemID = submitBody.id ?? submitBody.item_id ?? submitBody.item?.id;
  expect(itemID).toBeGreaterThan(0);

  const transitionResponse = await admin.post(`/api/items/${itemID}/transition`, {
    headers: SEC_FETCH,
    data: { to_status_id: reviewStatusID },
  });
  expect(
    transitionResponse.ok(),
    `open approval for ${title}: ${await transitionResponse.text()}`
  ).toBeTruthy();

  const timelineResponse = await admin.get(`/api/items/${itemID}/approvals`, {
    headers: SEC_FETCH,
  });
  expect(timelineResponse.ok()).toBeTruthy();
  const timeline = await timelineResponse.json();
  expect(timeline).toHaveLength(1);
  return { itemID, approvalID: timeline[0].id };
}

test('portal approval UI is isolated by channel for shared customers and internal users', async ({
  request,
  mail,
  playwright,
  page,
}) => {
  mail.skipIfMissing();

  const stamp = Date.now();
  const prefix = `e2e-pacs-${stamp}`;
  const slugA = `${prefix}-a`;
  const slugB = `${prefix}-b`;
  const customerEmail = `${prefix}@windshift.test`;
  const workspace = await createWorkspaceViaAPI(request, generateWorkspace(`pacs-${stamp}`));
  const fixture = await buildApprovalFixture(request, prefix, workspace.id);
  await createPortal(request, slugA, workspace.id);
  await createPortal(request, slugB, workspace.id);

  const loginCustomer = async (slug: string) => {
    const actor = await playwright.request.newContext({
      baseURL: BASE_URL,
      storageState: { cookies: [], origins: [] },
    });
    // Mailpit reports Created at second precision. Give the lower bound a
    // small cushion so a message delivered in this same second is not older
    // than the millisecond-precision client timestamp.
    const since = new Date(Date.now() - 2_000);
    const magicResponse = await actor.post(`/api/portal/${slug}/auth/request`, {
      headers: SEC_FETCH,
      data: { email: customerEmail },
    });
    expect(magicResponse.ok(), `request ${slug} login: ${await magicResponse.text()}`).toBeTruthy();
    const message = await mail.waitForLast({
      to: customerEmail,
      subject: 'Sign in to your portal',
      since,
      timeoutMs: 5000,
    });
    const token = message.Text.match(/[?#&]token=([A-Za-z0-9_=-]+)/)?.[1];
    expect(token, `magic token missing for ${slug}`).toBeTruthy();
    if (!token) throw new Error(`magic token missing for ${slug}`);
    const verifyResponse = await actor.get(
      `/api/portal/${slug}/auth/verify?token=${encodeURIComponent(token)}`,
      { headers: SEC_FETCH }
    );
    expect(
      verifyResponse.ok(),
      `verify ${slug} login: ${await verifyResponse.text()}`
    ).toBeTruthy();
    return actor;
  };

  const customerA = await loginCustomer(slugA);
  const customerB = await loginCustomer(slugB);

  const customerAApprove = await submitAndOpenApproval(
    customerA,
    request,
    slugA,
    `${prefix} customer A approve`,
    fixture.reviewStatusID
  );
  const customerAReject = await submitAndOpenApproval(
    customerA,
    request,
    slugA,
    `${prefix} customer A reject`,
    fixture.reviewStatusID
  );
  const customerBPending = await submitAndOpenApproval(
    customerB,
    request,
    slugB,
    `${prefix} customer B pending`,
    fixture.reviewStatusID
  );
  const internalA = await submitAndOpenApproval(
    request,
    request,
    slugA,
    `${prefix} internal A`,
    fixture.reviewStatusID
  );
  const internalB = await submitAndOpenApproval(
    request,
    request,
    slugB,
    `${prefix} internal B`,
    fixture.reviewStatusID
  );

  const browser = await playwright.chromium.launch();
  const customerAContext = await browser.newContext({
    storageState: await customerA.storageState(),
  });
  const customerAPage = await customerAContext.newPage();
  await customerAPage.goto(`${BASE_URL}/portal/${slugA}?view=approvals`);
  await expect(customerAPage.getByTestId('portal-my-approvals')).toBeVisible();
  await expect(
    customerAPage.locator(`#portal-approval-row-${customerAApprove.approvalID}`)
  ).toBeVisible();
  await expect(
    customerAPage.locator(`#portal-approval-row-${customerAReject.approvalID}`)
  ).toBeVisible();
  await expect(
    customerAPage.locator(`#portal-approval-row-${customerBPending.approvalID}`)
  ).toHaveCount(0);

  await customerAPage.locator(`#portal-approval-row-${customerAApprove.approvalID}`).click();
  await expect(customerAPage.getByTestId('portal-approval-item-context')).toContainText(
    `${prefix} customer A approve`
  );
  await customerAPage.getByTestId('portal-approval-comment').fill('channel A browser comment');
  await customerAPage.getByTestId('portal-approval-comment-submit').click();
  await expect(customerAPage.getByTestId('portal-approval-audit')).toContainText(
    'channel A browser comment'
  );

  customerAPage.once('dialog', (dialog) => dialog.accept());
  await customerAPage.getByTestId('portal-approval-approve').click();
  await expect(customerAPage.getByTestId('portal-approval-decide')).toHaveCount(0);
  await customerAPage.locator('#portal-approval-close').click();
  await expect(
    customerAPage.locator(`#portal-approval-row-${customerAApprove.approvalID}`)
  ).toHaveCount(0);

  await customerAPage.locator(`#portal-approval-row-${customerAReject.approvalID}`).click();
  await expect(customerAPage.getByTestId('portal-approval-item-context')).toContainText(
    `${prefix} customer A reject`
  );
  customerAPage.once('dialog', (dialog) => dialog.accept());
  await customerAPage.getByTestId('portal-approval-reject').click();
  await expect(customerAPage.getByTestId('portal-approval-decide')).toHaveCount(0);

  await customerAPage.goto(`${BASE_URL}/portal/${slugB}?view=approvals`);
  await expect(customerAPage.getByTestId('portal-page')).toContainText(/portal|authentication/i);

  const customerBContext = await browser.newContext({
    storageState: await customerB.storageState(),
  });
  const customerBPage = await customerBContext.newPage();
  await customerBPage.goto(`${BASE_URL}/portal/${slugB}?view=approvals`);
  await expect(
    customerBPage.locator(`#portal-approval-row-${customerBPending.approvalID}`)
  ).toBeVisible();
  await expect(
    customerBPage.locator(`#portal-approval-row-${customerAApprove.approvalID}`)
  ).toHaveCount(0);
  await customerBPage.locator(`#portal-approval-row-${customerBPending.approvalID}`).click();
  await expect(customerBPage.getByTestId('portal-approval-item-context')).toContainText(
    `${prefix} customer B pending`
  );

  await page.goto(`/portal/${slugA}?view=approvals`);
  await expect(page.locator(`#portal-approval-row-${internalA.approvalID}`)).toBeVisible();
  await expect(page.locator(`#portal-approval-row-${internalB.approvalID}`)).toHaveCount(0);
  await page.locator(`#portal-approval-row-${internalA.approvalID}`).click();
  await expect(page.getByTestId('portal-approval-item-context')).toContainText(
    `${prefix} internal A`
  );

  await page.goto(`/portal/${slugB}?view=approvals`);
  await expect(page.locator(`#portal-approval-row-${internalB.approvalID}`)).toBeVisible();
  await expect(page.locator(`#portal-approval-row-${internalA.approvalID}`)).toHaveCount(0);

  await customerAContext.close();
  await customerBContext.close();
  await browser.close();
  await customerA.dispose();
  await customerB.dispose();
});
