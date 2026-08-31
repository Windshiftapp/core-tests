import type { Page, Response } from '@playwright/test';
import { createUserViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/role-context';
import { generateUser, generateWorkspace } from '../fixtures/test-data';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

type Identified = { id: number };

function waitForResponse(page: Page, method: string, pathname: string): Promise<Response> {
  return page.waitForResponse((response) => {
    const request = response.request();
    return request.method() === method && new URL(response.url()).pathname === pathname;
  });
}

async function responseBody<T>(response: Response, operation: string): Promise<T> {
  if (!response.ok()) {
    const body = await response.text().catch(() => '<response body unavailable>');
    expect(response.ok(), `${operation} failed (${response.status()}): ${body}`).toBeTruthy();
  }
  return response.json() as Promise<T>;
}

async function expectResponseOK(response: Response, operation: string): Promise<void> {
  if (response.ok()) return;
  const body = await response.text().catch(() => '<response body unavailable>');
  expect(response.ok(), `${operation} failed (${response.status()}): ${body}`).toBeTruthy();
}

async function fillRichEditor(page: Page, testid: string, value: string): Promise<void> {
  const editor = page.getByTestId(testid);
  await expect(editor).toBeVisible({ timeout: 15_000 });
  await expect(editor).toHaveAttribute('data-ready', 'true', {
    timeout: 15_000,
  });
  await editor.click();
  await page.keyboard.insertText(value);
  await expect(editor).toContainText(value);
}

async function createCaseWithStep(
  page: Page,
  workspaceId: number,
  title: string,
  action: string,
  expected: string
): Promise<{ testCase: Identified; step: Identified }> {
  await page.getByTestId('test-case-create-button').click();
  await page.getByTestId('test-case-title').fill(title);
  await page.getByTestId('test-case-preconditions').fill(`Precondition for ${title}`);

  const caseResponsePromise = waitForResponse(
    page,
    'POST',
    `/api/workspaces/${workspaceId}/test-cases`
  );
  await page.getByTestId('test-case-submit').click();
  const testCase = await responseBody<Identified>(await caseResponsePromise, 'create test case');
  await expect(page.getByTestId(`test-case-row-${testCase.id}`)).toBeVisible();

  await page.getByTestId(`test-case-steps-${testCase.id}`).click();
  await page.getByTestId('test-step-create-button').click();
  await fillRichEditor(page, 'test-step-action-editor', action);
  await fillRichEditor(page, 'test-step-data-editor', `Data for ${title}`);
  await fillRichEditor(page, 'test-step-expected-editor', expected);

  const submit = page.getByTestId('test-step-submit');
  await expect(submit).toBeEnabled();
  const stepResponsePromise = waitForResponse(
    page,
    'POST',
    `/api/workspaces/${workspaceId}/test-cases/${testCase.id}/steps`
  );
  await submit.click();
  const step = await responseBody<Identified>(await stepResponsePromise, 'create test step');
  await expect(page.getByTestId(`test-step-row-${step.id}`)).toBeVisible();
  await page.getByTestId('test-steps-back').click();

  return { testCase, step };
}

test.describe('Test management browser lifecycle', () => {
  test('creates cases and a plan, resumes mixed execution, reports evidence, and starts a rerun', async ({
    page,
    request,
  }) => {
    const workspace = await createWorkspaceViaAPI(request, generateWorkspace('test-lifecycle'));
    const stamp = Date.now();
    const passedTitle = `Checkout passes ${stamp}`;
    const failedTitle = `Checkout fails ${stamp}`;
    const planName = `Checkout plan ${stamp}`;
    const runName = `Checkout run ${stamp}`;
    const rerunName = `Checkout rerun ${stamp}`;
    const failureEvidence = `Observed HTTP 502 ${stamp}`;
    const failureNotes = `Reproduces against checkout service ${stamp}`;

    await page.goto(`/workspaces/${workspace.id}/tests`);
    const passed = await createCaseWithStep(
      page,
      workspace.id,
      passedTitle,
      'Submit a valid checkout',
      'The order confirmation is displayed'
    );
    const failed = await createCaseWithStep(
      page,
      workspace.id,
      failedTitle,
      'Submit checkout during an upstream failure',
      'The customer can retry safely'
    );

    await page.goto(`/workspaces/${workspace.id}/tests/sets`);
    await page.getByTestId('test-set-create-button').click();
    await page.getByTestId('test-set-name').fill(planName);
    await page.getByTestId('test-set-description').fill('Browser-driven release checkout plan');
    const setResponsePromise = waitForResponse(
      page,
      'POST',
      `/api/workspaces/${workspace.id}/test-sets`
    );
    await page.getByTestId('test-set-submit').click();
    const testSet = await responseBody<Identified>(await setResponsePromise, 'create test plan');
    await expect(page.getByTestId(`test-set-row-${testSet.id}`)).toBeVisible();

    await page.getByTestId(`test-set-actions-${testSet.id}`).click();
    await page.getByTestId(`test-set-manage-${testSet.id}`).click();
    for (const testCase of [passed.testCase, failed.testCase]) {
      await page.locator('#test-case-picker').click();
      const linkResponsePromise = waitForResponse(
        page,
        'POST',
        `/api/workspaces/${workspace.id}/test-sets/${testSet.id}/test-cases`
      );
      await page.getByTestId(`test-case-picker-option-${testCase.id}`).click();
      await expectResponseOK(await linkResponsePromise, 'add test case to plan');
      await expect(page.getByTestId(`test-set-case-${testCase.id}`)).toBeVisible();
    }
    await page.getByTestId('test-set-manage-done').click();

    await page.goto(`/workspaces/${workspace.id}/tests/runs`);
    await page.getByTestId('create-test-run-button').click();
    await page.locator('#set-select').click();
    await page.locator(`#set-select-option-${testSet.id}`).click();
    await page.locator('#run-name').fill(runName);
    const runResponsePromise = waitForResponse(
      page,
      'POST',
      `/api/workspaces/${workspace.id}/test-runs`
    );
    await page.getByTestId('create-run-submit').click();
    const run = await responseBody<Identified>(await runResponsePromise, 'create test run');
    await expect(page.getByTestId(`test-run-row-${run.id}`)).toBeVisible();

    await page.getByTestId(`test-run-actions-${run.id}`).click();
    await page.getByTestId(`test-run-continue-${run.id}`).click();
    const execution = page.getByTestId('test-execution');
    await expect(execution).toHaveAttribute('data-current-case-id', String(passed.testCase.id));
    await expect(execution).toHaveAttribute('data-current-step-id', String(passed.step.id));

    const passResponsePromise = waitForResponse(
      page,
      'PUT',
      `/api/workspaces/${workspace.id}/test-runs/${run.id}/steps/${passed.step.id}`
    );
    await page.getByTestId('test-execution-status-passed').click();
    await responseBody(await passResponsePromise, 'record passed result');
    await expect(execution).toHaveAttribute('data-current-case-id', String(failed.testCase.id));
    await expect(page.getByTestId(`test-execution-case-${passed.testCase.id}`)).toHaveAttribute(
      'data-progress',
      '100'
    );

    await page.getByTestId('test-execution-back').click();
    await expect(page.getByTestId(`test-run-row-${run.id}`)).toBeVisible();
    await page.getByTestId(`test-run-actions-${run.id}`).click();
    await page.getByTestId(`test-run-continue-${run.id}`).click();
    await expect(execution).toHaveAttribute('data-current-case-id', String(failed.testCase.id));
    await expect(execution).toHaveAttribute('data-current-step-id', String(failed.step.id));

    const evidenceResponsePromise = waitForResponse(
      page,
      'PUT',
      `/api/workspaces/${workspace.id}/test-runs/${run.id}/steps/${failed.step.id}`
    );
    await fillRichEditor(page, 'test-execution-actual-result-editor', failureEvidence);
    await responseBody(await evidenceResponsePromise, 'save result evidence');

    const notesResponsePromise = waitForResponse(
      page,
      'PUT',
      `/api/workspaces/${workspace.id}/test-runs/${run.id}/steps/${failed.step.id}`
    );
    await fillRichEditor(page, 'test-execution-notes-editor', failureNotes);
    await responseBody(await notesResponsePromise, 'save result notes');

    const failResponsePromise = waitForResponse(
      page,
      'PUT',
      `/api/workspaces/${workspace.id}/test-runs/${run.id}/steps/${failed.step.id}`
    );
    await page.getByTestId('test-execution-status-failed').click();
    await responseBody(await failResponsePromise, 'record failed result');
    await expect(page.getByTestId('test-execution-current-status')).toContainText('Failed');

    await page.getByTestId('test-execution-finish-current').click();
    const endResponsePromise = waitForResponse(
      page,
      'POST',
      `/api/workspaces/${workspace.id}/test-runs/${run.id}/end`
    );
    await page.getByTestId('dialog-confirm').click();
    await expectResponseOK(await endResponsePromise, 'end test run');
    await expect(page.getByTestId(`test-run-row-${run.id}`)).toBeVisible();

    await page.goto(`/workspaces/${workspace.id}/tests/reports`);
    await expect(page.getByTestId('test-report-total')).toHaveText('2');
    await expect(page.getByTestId('test-report-passed')).toHaveText('1');
    await expect(page.getByTestId('test-report-failed')).toHaveText('1');
    await expect(page.getByTestId('test-report-pass-rate')).toHaveText('50.0%');
    await page.getByTestId(`test-report-failure-run-${run.id}`).click();

    await expect(page.getByTestId(`test-run-result-${passed.testCase.id}`)).toContainText('Passed');
    await expect(page.getByTestId(`test-run-result-${failed.testCase.id}`)).toContainText('Failed');
    await expect(page.getByTestId(`test-run-step-actual-${failed.step.id}`)).toContainText(
      failureEvidence
    );
    await expect(page.getByTestId(`test-run-step-notes-${failed.step.id}`)).toContainText(
      failureNotes
    );

    await page.context().addInitScript(() => {
      // @ts-expect-error neutralize the native print dialog in headless runs
      window.print = () => {};
    });
    const summaryPopupPromise = page.waitForEvent('popup');
    await page.getByTestId('test-run-export-results').click();
    const summaryPopup = await summaryPopupPromise;
    await expect
      .poll(() => summaryPopup.url())
      .toMatch(new RegExp(`/workspaces/${workspace.id}/tests/runs/${run.id}/print$`));
    const summaryBody = summaryPopup.getByTestId('test-run-summary-print-body');
    await expect(summaryBody).toContainText(runName, { timeout: 15_000 });
    await expect(summaryBody).toContainText('Statistics');
    expect(await summaryBody.evaluate((body) => body.querySelectorAll('table').length)).toBe(2);
    await expect(summaryPopup.getByTestId('test-run-summary-print-button')).toBeVisible();
    await summaryPopup.close();

    page.once('dialog', (dialog) => dialog.accept(rerunName));
    const rerunResponsePromise = waitForResponse(
      page,
      'POST',
      `/api/workspaces/${workspace.id}/test-runs`
    );
    await page.getByTestId('test-run-rerun').click();
    const rerun = await responseBody<Identified>(await rerunResponsePromise, 'create rerun');
    await expect(execution).toHaveAttribute('data-run-id', String(rerun.id));
    await page.getByTestId('test-execution-back').click();
    await expect(page.getByTestId(`test-run-row-${run.id}`)).toBeVisible();
    await expect(page.getByTestId(`test-run-row-${rerun.id}`)).toBeVisible();

    await page.goto(`/workspaces/${workspace.id}/tests/sets`);
    await expect(page.getByTestId(`test-set-row-${testSet.id}`)).toContainText('2 total');
  });

  // WI-390 (GH #134): selecting an assignee used to make run creation fail
  // silently for active users without an explicit workspace role.
  test('creates a run with an assignee from the Test Runs section', async ({ page, getCtx }) => {
    const ctx = await getCtx('admin');
    const ws = ctx.workspaceId;
    const stamp = `${Date.now()}`;

    const caseResp = await ctx.request.post(`/api/workspaces/${ws}/test-cases`, {
      headers: SEC_FETCH,
      data: {
        title: `E2E TC assignee ${stamp}`,
        preconditions: '',
        priority: 'medium',
        status: 'active',
        estimated_duration: 0,
      },
    });
    expect(caseResp.ok()).toBeTruthy();
    const testCase = await caseResp.json();

    const setResp = await ctx.request.post(`/api/workspaces/${ws}/test-sets`, {
      headers: SEC_FETCH,
      data: { name: `E2E TS assignee ${stamp}`, description: '' },
    });
    expect(setResp.ok()).toBeTruthy();
    const testSet = await setResp.json();

    const linkResp = await ctx.request.post(
      `/api/workspaces/${ws}/test-sets/${testSet.id}/test-cases`,
      { headers: SEC_FETCH, data: { test_case_id: testCase.id } }
    );
    expect(linkResp.ok()).toBeTruthy();

    const assigneeData = generateUser('wi390');
    const assignee = await createUserViaAPI(ctx.request, assigneeData);

    await page.goto(`/workspaces/${ws}/tests/runs`);
    await page.getByTestId('create-test-run-button').click();
    await page.locator('#set-select').click();
    await page.locator(`#set-select-option-${testSet.id}`).click();
    await page.locator('#run-name').fill(`E2E UI Run ${stamp}`);

    await page.getByTestId('user-picker-trigger').click();
    await page.getByTestId('user-picker-search').fill(assigneeData.username);
    await page.getByTestId(`user-picker-option-${assignee.id}`).click();

    const runResponsePromise = waitForResponse(page, 'POST', `/api/workspaces/${ws}/test-runs`);
    await page.getByTestId('create-run-submit').click();
    const run = await responseBody<Identified>(
      await runResponsePromise,
      'create assigned test run'
    );

    await expect(page.getByTestId('create-run-submit')).toBeHidden();
    await expect(page.getByTestId(`test-run-row-${run.id}`)).toContainText(assigneeData.last_name);
  });
});
