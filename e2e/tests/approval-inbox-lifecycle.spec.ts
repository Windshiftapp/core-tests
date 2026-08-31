import { createItemViaAPI, createUserViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { type APIRequestContext, type Browser, expect, test } from '../fixtures/context-path';
import { generateUser, generateWorkspace, type TestUser } from '../fixtures/test-data';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

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

async function getWorkspaceRole(request: APIRequestContext, name: string): Promise<number> {
  const response = await request.get('/api/workspace-roles', {
    headers: SEC_FETCH,
  });
  expect(response.ok()).toBeTruthy();
  const body = await response.json();
  const roles = (body.data ?? body) as Array<{ id: number; name: string }>;
  const role = roles.find((candidate) => candidate.name === name);
  expect(role, `workspace role ${name} missing`).toBeDefined();
  if (!role) throw new Error(`workspace role ${name} missing`);
  return role.id;
}

async function assignRole(
  request: APIRequestContext,
  userID: number,
  workspaceID: number,
  roleID: number
) {
  const response = await request.post('/api/workspace-roles/assign', {
    headers: SEC_FETCH,
    data: {
      user_id: userID,
      workspace_id: workspaceID,
      role_id: roleID,
    },
  });
  expect(
    response.ok(),
    `assign role ${roleID} to user ${userID}: ${await response.text()}`
  ).toBeTruthy();
}

async function loginBrowser(browser: Browser, user: TestUser) {
  const context = await browser.newContext({
    baseURL: BASE_URL,
    storageState: { cookies: [], origins: [] },
  });
  const response = await context.request.post('/api/auth/login', {
    headers: SEC_FETCH,
    data: {
      email_or_username: user.username,
      password: user.password_hash,
      remember_me: false,
    },
  });
  expect(response.ok(), `login ${user.username}: ${await response.text()}`).toBeTruthy();
  return context;
}

test('sequential approval inbox resolves a changed role and recovers a failed final decision', async ({
  request,
  browser,
}) => {
  test.setTimeout(90_000);
  const stamp = Date.now();
  const prefix = `approval-inbox-${stamp}`;
  const workspace = await createWorkspaceViaAPI(request, generateWorkspace(prefix));
  // Keep generated identity fields below the API's field-length limits while
  // retaining the timestamp and per-user distinction for parallel runs.
  const firstApproverData = generateUser(`aif-${stamp}`);
  const removedApproverData = generateUser(`air-${stamp}`);
  const replacementApproverData = generateUser(`aip-${stamp}`);
  const firstApprover = await createUserViaAPI(request, firstApproverData);
  const removedApprover = await createUserViaAPI(request, removedApproverData);
  const replacementApprover = await createUserViaAPI(request, replacementApproverData);
  const editorRoleID = await getWorkspaceRole(request, 'Editor');
  await assignRole(request, removedApprover.id, workspace.id, editorRoleID);

  const categoriesResponse = await request.get('/api/status-categories', {
    headers: SEC_FETCH,
  });
  expect(categoriesResponse.ok()).toBeTruthy();
  const categories = (await categoriesResponse.json()) as Array<{
    id: number;
    is_default: boolean;
  }>;
  const categoryID = categories.find((category) => category.is_default)?.id ?? categories[0]?.id;
  expect(categoryID).toBeGreaterThan(0);
  const reviewStatusID = await createStatus(request, `${prefix}-Review`, categoryID);
  const approvedStatusID = await createStatus(request, `${prefix}-Approved`, categoryID);
  const rejectedStatusID = await createStatus(request, `${prefix}-Rejected`, categoryID);

  const workflowResponse = await request.post('/api/workflows', {
    headers: SEC_FETCH,
    data: { name: `${prefix}-workflow`, description: 'approval inbox e2e' },
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
    `configure workflow: ${await transitionsResponse.text()}`
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
  expect(approveTransitionID).toBeGreaterThan(0);
  expect(denyTransitionID).toBeGreaterThan(0);

  const approvalSetResponse = await request.post('/api/approval-sets', {
    headers: SEC_FETCH,
    data: {
      name: `${prefix}-set`,
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
              name: 'Named reviewer',
              quorum_mode: 'any',
              approver_source: 'user',
              approver_user_id: firstApprover.id,
              allow_self_approval: false,
              on_leave_strategy: 'keep',
            },
            {
              display_order: 1,
              name: 'Current editor',
              quorum_mode: 'any',
              approver_source: 'role',
              approver_role_id: editorRoleID,
              allow_self_approval: false,
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
      workspace_ids: [workspace.id],
    },
  });
  expect(
    configurationResponse.ok(),
    `create configuration: ${await configurationResponse.text()}`
  ).toBeTruthy();

  const item = await createItemViaAPI(request, workspace.id, {
    title: `Approval role change ${stamp}`,
    description: `Approval recovery ${stamp}`,
  });
  const transitionResponse = await request.post(`/api/items/${item.id}/transition`, {
    headers: SEC_FETCH,
    data: { to_status_id: reviewStatusID },
  });
  expect(transitionResponse.ok(), `open approval: ${await transitionResponse.text()}`).toBeTruthy();

  const timelineResponse = await request.get(`/api/items/${item.id}/approvals`, {
    headers: SEC_FETCH,
  });
  expect(timelineResponse.ok()).toBeTruthy();
  const timeline = await timelineResponse.json();
  expect(timeline).toHaveLength(1);
  const approvalID = timeline[0].id as number;
  const firstStepID = timeline[0].step_instances[0].id as number;
  const secondStepID = timeline[0].step_instances[1].id as number;

  const revokeResponse = await request.delete(
    `/api/users/${removedApprover.id}/workspaces/${workspace.id}/roles/${editorRoleID}`,
    { headers: SEC_FETCH }
  );
  expect(
    revokeResponse.ok() || revokeResponse.status() === 204,
    `remove original role member: ${await revokeResponse.text()}`
  ).toBeTruthy();
  await assignRole(request, replacementApprover.id, workspace.id, editorRoleID);

  const firstContext = await loginBrowser(browser, firstApproverData);
  const removedContext = await loginBrowser(browser, removedApproverData);
  const replacementContext = await loginBrowser(browser, replacementApproverData);
  try {
    const firstPage = await firstContext.newPage();
    await firstPage.goto('/approvals');
    const firstInboxRow = firstPage.getByTestId(`approval-inbox-row-${approvalID}`);
    await expect(firstInboxRow).toBeVisible();
    await firstInboxRow.getByTestId('approval-inbox-open').click();
    await expect(firstPage).toHaveURL(
      new RegExp(`/workspaces/${workspace.id}/items/${item.id}(?:$|[?#])`)
    );
    await expect(firstPage.getByTestId(`approval-request-${approvalID}`)).toBeVisible();
    await firstPage.getByTestId('approval-decision-comment').fill(`First step approved ${stamp}`);
    const firstDecision = firstPage.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/approvals/${approvalID}/decide`) &&
        response.request().method() === 'POST'
    );
    await firstPage.getByTestId('approval-decision-approve').click();
    await firstPage.getByTestId('dialog-confirm').click();
    expect((await firstDecision).ok()).toBeTruthy();

    const removedPage = await removedContext.newPage();
    await removedPage.goto('/approvals');
    await expect(removedPage.getByTestId(`approval-inbox-row-${approvalID}`)).toHaveCount(0);

    const replacementPage = await replacementContext.newPage();
    await replacementPage.goto('/approvals');
    const replacementInboxRow = replacementPage.getByTestId(`approval-inbox-row-${approvalID}`);
    await expect(replacementInboxRow).toBeVisible();
    await replacementInboxRow.getByTestId('approval-inbox-open').click();
    await expect(replacementPage.getByTestId(`approval-step-${firstStepID}`)).toHaveAttribute(
      'data-step-status',
      'approved'
    );
    const replacementStep = replacementPage.getByTestId(`approval-step-${secondStepID}`);
    await expect(replacementStep).toHaveAttribute('data-step-status', 'pending');
    await expect(replacementStep).toContainText(`#${replacementApprover.id}`);
    await expect(replacementStep).not.toContainText(`#${removedApprover.id}`);

    let decisionAttempts = 0;
    await replacementPage.route(`**/api/approvals/${approvalID}/decide`, async (route) => {
      decisionAttempts += 1;
      if (decisionAttempts === 1) {
        await route.fulfill({
          status: 503,
          contentType: 'application/json',
          body: JSON.stringify({ error: 'Temporary approval outage' }),
        });
        return;
      }
      await route.continue();
    });
    const finalComment = `Retained final decision ${stamp}`;
    await replacementPage.getByTestId('approval-decision-comment').fill(finalComment);
    await replacementPage.getByTestId('approval-decision-approve').click();
    await replacementPage.getByTestId('dialog-confirm').click();
    const errorToast = replacementPage.getByTestId('toast');
    await expect(errorToast).toHaveAttribute('data-toast-variant', 'error');
    await expect(errorToast).toContainText('Temporary approval outage');
    await expect(replacementPage.getByTestId('approval-decision-comment')).toHaveValue(
      finalComment
    );
    await expect(replacementPage.getByTestId('approval-decision-approve')).toBeVisible();

    const finalDecision = replacementPage.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/approvals/${approvalID}/decide`) &&
        response.request().method() === 'POST' &&
        response.status() === 200
    );
    await replacementPage.getByTestId('approval-decision-approve').click();
    await replacementPage.getByTestId('dialog-confirm').click();
    await finalDecision;
    expect(decisionAttempts).toBe(2);
    const completedRequest = replacementPage.getByTestId(`approval-request-${approvalID}`);
    await expect(completedRequest).toContainText('Approved');
    await expect(replacementPage.getByTestId(`approval-step-${secondStepID}`)).toHaveAttribute(
      'data-step-status',
      'approved'
    );
  } finally {
    await Promise.allSettled([
      firstContext.close(),
      removedContext.close(),
      replacementContext.close(),
    ]);
  }
});
