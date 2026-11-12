import type {
	APIRequestContext,
	APIResponse,
	Page,
	Response,
} from "@playwright/test";
import {
	createItemViaAPI,
	createUserViaAPI,
	createWorkspaceViaAPI,
} from "../fixtures/api-helpers";
import { test as base, expect } from "../fixtures/role-context";
import { generateUser, generateWorkspace } from "../fixtures/test-data";

const SEC_FETCH = { "Sec-Fetch-Site": "same-origin" };

type Identified = { id: number };

function waitForResponse(
	page: Page,
	method: string,
	pathname: string,
): Promise<Response> {
	return page.waitForResponse((response) => {
		const request = response.request();
		return (
			request.method() === method &&
			new URL(response.url()).pathname === pathname
		);
	});
}

async function responseBody<T>(
	response: Response | APIResponse,
	operation: string,
): Promise<T> {
	if (!response.ok()) {
		const body = await response
			.text()
			.catch(() => "<response body unavailable>");
		expect(
			response.ok(),
			`${operation} failed (${response.status()}): ${body}`,
		).toBeTruthy();
	}
	const body = await response.json();
	return (body.data ?? body) as T;
}

async function expectResponseOK(
	response: Response | APIResponse,
	operation: string,
): Promise<void> {
	if (response.ok()) return;
	const body = await response.text().catch(() => "<response body unavailable>");
	expect(
		response.ok(),
		`${operation} failed (${response.status()}): ${body}`,
	).toBeTruthy();
}

async function fillRichEditor(
	page: Page,
	testid: string,
	value: string,
): Promise<void> {
	const editor = page.getByTestId(testid);
	await expect(editor).toBeVisible({ timeout: 15_000 });
	await expect(editor).toHaveAttribute("data-ready", "true", {
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
	expected: string,
): Promise<{ testCase: Identified; step: Identified }> {
	await page.getByTestId("test-case-create-button").click();
	await page.getByTestId("test-case-title").fill(title);
	await page
		.getByTestId("test-case-preconditions")
		.fill(`Precondition for ${title}`);

	const caseResponsePromise = waitForResponse(
		page,
		"POST",
		`/api/v2/workspaces/${workspaceId}/test-cases`,
	);
	await page.getByTestId("test-case-submit").click();
	const testCase = await responseBody<Identified>(
		await caseResponsePromise,
		"create test case",
	);
	await expect(page.getByTestId(`test-case-row-${testCase.id}`)).toBeVisible();

	await page.getByTestId(`test-case-steps-${testCase.id}`).click();
	await page.getByTestId("test-step-create-button").click();
	await fillRichEditor(page, "test-step-action-editor", action);
	await fillRichEditor(page, "test-step-data-editor", `Data for ${title}`);
	await fillRichEditor(page, "test-step-expected-editor", expected);

	const submit = page.getByTestId("test-step-submit");
	await expect(submit).toBeEnabled();
	const stepResponsePromise = waitForResponse(
		page,
		"POST",
		`/api/v2/workspaces/${workspaceId}/test-cases/${testCase.id}/steps`,
	);
	await submit.click();
	const step = await responseBody<Identified>(
		await stepResponsePromise,
		"create test step",
	);
	await expect(page.getByTestId(`test-step-row-${step.id}`)).toBeVisible();
	await page.getByTestId("test-steps-back").click();

	return { testCase, step };
}

const test = base.extend<{ lifecycleWorkspace: Identified }>({
	lifecycleWorkspace: async ({ request }, use) => {
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace("test-lifecycle"),
		);
		try {
			await use(workspace);
		} finally {
			const deleted = await request.delete(
				`/api/v2/workspaces/${workspace.id}`,
				{
					headers: SEC_FETCH,
				},
			);
			expect(deleted.status()).toBe(204);
		}
	},
});

function lifecycleNames(workspaceId: number) {
	return {
		passedTitle: `Checkout passes ${workspaceId}`,
		failedTitle: `Checkout fails ${workspaceId}`,
		planName: `Checkout plan ${workspaceId}`,
		runName: `Checkout run ${workspaceId}`,
		rerunName: `Checkout rerun ${workspaceId}`,
		failureEvidence: `Observed HTTP 502 ${workspaceId}`,
		failureNotes: `Reproduces against checkout service ${workspaceId}`,
	};
}

async function seedLifecyclePlan(
	request: APIRequestContext,
	workspaceId: number,
) {
	const { passedTitle, failedTitle, planName } = lifecycleNames(workspaceId);
	const root = `/api/v2/workspaces/${workspaceId}`;
	async function create(path: string, data: Record<string, unknown>) {
		return responseBody<Identified>(
			await request.post(`${root}${path}`, { headers: SEC_FETCH, data }),
			`seed ${path}`,
		);
	}
	async function createCase(title: string) {
		const testCase = await create("/test-cases", {
			title,
			priority: "medium",
			status: "active",
		});
		const step = await create(`/test-cases/${testCase.id}/steps`, {
			action: `Execute ${title}`,
			data: "",
			expected: "Checkout succeeds",
		});
		return { testCase, step };
	}
	const passed = await createCase(passedTitle);
	const failed = await createCase(failedTitle);
	const testSet = await create("/test-plans", {
		name: planName,
		description: "",
	});
	for (const testCase of [passed.testCase, failed.testCase]) {
		await expectResponseOK(
			await request.post(`${root}/test-plans/${testSet.id}/test-cases`, {
				headers: SEC_FETCH,
				data: { test_case_id: testCase.id },
			}),
			"seed plan membership",
		);
	}
	return { passed, failed, testSet };
}

test.describe("Test management browser lifecycle", () => {
	test("creates cases with steps and persists their plan membership", async ({
		page,
		lifecycleWorkspace: workspace,
	}) => {
		const { passedTitle, failedTitle, planName } = lifecycleNames(workspace.id);
		const unrelatedMilestoneRequests: string[] = [];
		page.on("request", (request) => {
			const path = new URL(request.url()).pathname;
			const match = path.match(/\/api\/v2\/workspaces\/(\d+)\/milestones$/);
			if (match && Number(match[1]) !== workspace.id)
				unrelatedMilestoneRequests.push(path);
		});
		await page.goto(`/workspaces/${workspace.id}/tests`);
		const passed = await createCaseWithStep(
			page,
			workspace.id,
			passedTitle,
			"Submit a valid checkout",
			"The order confirmation is displayed",
		);
		const failed = await createCaseWithStep(
			page,
			workspace.id,
			failedTitle,
			"Submit checkout during an upstream failure",
			"The customer can retry safely",
		);

		await page.goto(`/workspaces/${workspace.id}/tests/sets`);
		await page.getByTestId("test-set-create-button").click();
		await page.getByTestId("test-set-name").fill(planName);
		await page
			.getByTestId("test-set-description")
			.fill("Browser-driven release checkout plan");
		const setResponsePromise = waitForResponse(
			page,
			"POST",
			`/api/v2/workspaces/${workspace.id}/test-plans`,
		);
		await page.getByTestId("test-set-submit").click();
		const testSet = await responseBody<Identified>(
			await setResponsePromise,
			"create test plan",
		);
		await expect(page.getByTestId(`test-set-row-${testSet.id}`)).toBeVisible();
		expect(unrelatedMilestoneRequests).toEqual([]);

		await page.getByTestId(`test-set-actions-${testSet.id}`).click();
		await page.getByTestId(`test-set-manage-${testSet.id}`).click();
		for (const testCase of [passed.testCase, failed.testCase]) {
			await page.locator("#test-case-picker").click();
			const linkResponsePromise = waitForResponse(
				page,
				"POST",
				`/api/v2/workspaces/${workspace.id}/test-plans/${testSet.id}/test-cases`,
			);
			await page.getByTestId(`test-case-picker-option-${testCase.id}`).click();
			await expectResponseOK(
				await linkResponsePromise,
				"add test case to plan",
			);
			await expect(
				page.getByTestId(`test-set-case-${testCase.id}`),
			).toBeVisible();
		}
		await page.getByTestId("test-set-manage-done").click();
		await page.goto(`/workspaces/${workspace.id}/tests/sets/${testSet.id}`);
		await expect(
			page.getByTestId(`test-set-case-${passed.testCase.id}`),
		).toBeVisible();
		await page.getByTestId("test-set-manage-done").click();
		await expect(page).toHaveURL(
			new RegExp(`/workspaces/${workspace.id}/tests/sets$`),
		);
	});

	test("creates a run, resumes mixed execution, and persists the recorded evidence", async ({
		page,
		request,
		lifecycleWorkspace: workspace,
	}) => {
		const { runName, failureEvidence, failureNotes } = lifecycleNames(
			workspace.id,
		);
		const { passed, failed, testSet } = await seedLifecyclePlan(
			request,
			workspace.id,
		);
		const linkedIssue = await createItemViaAPI(request, workspace.id, {
			title: `Checkout defect ${workspace.id}`,
		});
		await page.goto(`/workspaces/${workspace.id}/tests/runs`);
		await page.getByTestId("create-test-run-button").click();
		await page.locator("#set-select").click();
		await page.locator(`#set-select-option-${testSet.id}`).click();
		await page.locator("#run-name").fill(runName);
		const runResponsePromise = waitForResponse(
			page,
			"POST",
			`/api/v2/workspaces/${workspace.id}/test-runs`,
		);
		await page.getByTestId("create-run-submit").click();
		const run = await responseBody<Identified>(
			await runResponsePromise,
			"create test run",
		);
		await expect(page.getByTestId(`test-run-row-${run.id}`)).toBeVisible();

		await page.getByTestId(`test-run-actions-${run.id}`).click();
		await page.getByTestId(`test-run-continue-${run.id}`).click();
		const execution = page.getByTestId("test-execution");
		await expect(execution).toHaveAttribute(
			"data-current-case-id",
			String(passed.testCase.id),
		);
		await expect(execution).toHaveAttribute(
			"data-current-step-id",
			String(passed.step.id),
		);

		const passResponsePromise = waitForResponse(
			page,
			"PATCH",
			`/api/v2/workspaces/${workspace.id}/test-runs/${run.id}/steps/${passed.step.id}`,
		);
		await page.getByTestId("test-execution-status-passed").click();
		await responseBody(await passResponsePromise, "record passed result");
		await expect(execution).toHaveAttribute(
			"data-current-case-id",
			String(failed.testCase.id),
		);
		await expect(
			page.getByTestId(`test-execution-case-${passed.testCase.id}`),
		).toHaveAttribute("data-progress", "100");

		await page.getByTestId("test-execution-back").click();
		await expect(page.getByTestId(`test-run-row-${run.id}`)).toBeVisible();
		await page.getByTestId(`test-run-actions-${run.id}`).click();
		await page.getByTestId(`test-run-continue-${run.id}`).click();
		await expect(execution).toHaveAttribute(
			"data-current-case-id",
			String(failed.testCase.id),
		);
		await expect(execution).toHaveAttribute(
			"data-current-step-id",
			String(failed.step.id),
		);

		const evidenceResponsePromise = waitForResponse(
			page,
			"PATCH",
			`/api/v2/workspaces/${workspace.id}/test-runs/${run.id}/steps/${failed.step.id}`,
		);
		await fillRichEditor(
			page,
			"test-execution-actual-result-editor",
			failureEvidence,
		);
		await responseBody(await evidenceResponsePromise, "save result evidence");

		const notesResponsePromise = waitForResponse(
			page,
			"PATCH",
			`/api/v2/workspaces/${workspace.id}/test-runs/${run.id}/steps/${failed.step.id}`,
		);
		await fillRichEditor(page, "test-execution-notes-editor", failureNotes);
		await responseBody(await notesResponsePromise, "save result notes");

		const failResponsePromise = waitForResponse(
			page,
			"PATCH",
			`/api/v2/workspaces/${workspace.id}/test-runs/${run.id}/steps/${failed.step.id}`,
		);
		await page.getByTestId("test-execution-status-failed").click();
		await responseBody(await failResponsePromise, "record failed result");
		await expect(
			page.getByTestId("test-execution-current-status"),
		).toContainText("Failed");

		await page.getByTestId("item-picker-trigger").click();
		await page
			.getByTestId("test-execution-item-search")
			.fill(linkedIssue.title);
		const linkItemResponsePromise = waitForResponse(
			page,
			"PATCH",
			`/api/v2/workspaces/${workspace.id}/test-runs/${run.id}/steps/${failed.step.id}`,
		);
		await page.getByTestId(`test-execution-item-${linkedIssue.id}`).click();
		await expectResponseOK(
			await linkItemResponsePromise,
			"link defect to failed test step",
		);
		await expect(
			page.getByTestId("test-execution-linked-item"),
		).toHaveAttribute(
			"href",
			`/workspaces/${workspace.id}/items/${linkedIssue.id}`,
		);

		await page.getByTestId("test-execution-finish-sidebar").click();
		const endResponsePromise = waitForResponse(
			page,
			"POST",
			`/api/v2/workspaces/${workspace.id}/test-runs/${run.id}/end`,
		);
		await page.getByTestId("dialog-confirm").click();
		await expectResponseOK(await endResponsePromise, "end test run");
		// Ended runs stay listed (the API includes them); the status flips to
		// Completed instead of the row disappearing.
		await expect(page.getByTestId(`test-run-row-${run.id}`)).toContainText(
			"Completed",
		);

		await page.goto(`/workspaces/${workspace.id}/tests/runs/${run.id}`);
		await expect(
			page.getByTestId(`test-run-result-${passed.testCase.id}`),
		).toContainText("Passed");
		await expect(
			page.getByTestId(`test-run-result-${failed.testCase.id}`),
		).toContainText("Failed");
		await expect(
			page.getByTestId(`test-run-step-actual-${failed.step.id}`),
		).toContainText(failureEvidence);
		await expect(
			page.getByTestId(`test-run-step-notes-${failed.step.id}`),
		).toContainText(failureNotes);
		await expect(
			page.getByTestId(`test-run-step-item-${failed.step.id}`),
		).toHaveAttribute(
			"href",
			`/workspaces/${workspace.id}/items/${linkedIssue.id}`,
		);
	});

	test("reports completed results, prints their summary, and starts an independent rerun", async ({
		page,
		request,
		lifecycleWorkspace: workspace,
	}) => {
		const { failedTitle, runName, rerunName, failureEvidence, failureNotes } =
			lifecycleNames(workspace.id);
		const { passed, failed, testSet } = await seedLifecyclePlan(
			request,
			workspace.id,
		);
		const linkedIssue = await createItemViaAPI(request, workspace.id, {
			title: `Checkout defect ${workspace.id}`,
		});
		const root = `/api/v2/workspaces/${workspace.id}/test-runs`;
		const run = await responseBody<Identified>(
			await request.post(root, {
				headers: SEC_FETCH,
				data: { plan_id: testSet.id, name: runName },
			}),
			"seed run",
		);
		for (const result of [
			{
				stepId: passed.step.id,
				status: "passed",
				actual_result: "",
				notes: "",
				item_id: null,
			},
			{
				stepId: failed.step.id,
				status: "failed",
				actual_result: failureEvidence,
				notes: failureNotes,
				item_id: linkedIssue.id,
			},
		]) {
			const { stepId, ...data } = result;
			await expectResponseOK(
				await request.patch(`${root}/${run.id}/steps/${stepId}`, {
					headers: {
						...SEC_FETCH,
						"Content-Type": "application/merge-patch+json",
					},
					data,
				}),
				"seed result",
			);
		}
		await expectResponseOK(
			await request.post(`${root}/${run.id}/end`, { headers: SEC_FETCH }),
			"end seeded run",
		);
		const execution = page.getByTestId("test-execution");
		await page.goto(`/workspaces/${workspace.id}/tests/reports`);
		await expect(page.getByTestId("test-report-total")).toHaveText("2");
		await expect(page.getByTestId("test-report-passed")).toHaveText("1");
		await expect(page.getByTestId("test-report-failed")).toHaveText("1");
		await expect(page.getByTestId("test-report-pass-rate")).toHaveText("50.0%");
		await page
			.getByTestId(`test-report-failure-case-${failed.testCase.id}`)
			.click();
		await expect(page.getByTestId("test-case-detail")).toContainText(
			failedTitle,
		);
		await page.getByTestId("test-case-detail-back").click();
		await expect(page).toHaveURL(
			new RegExp(`/workspaces/${workspace.id}/tests/reports$`),
		);
		await page.getByTestId(`test-report-failure-run-${run.id}`).click();

		await expect(
			page.getByTestId(`test-run-result-${passed.testCase.id}`),
		).toContainText("Passed");
		await expect(
			page.getByTestId(`test-run-result-${failed.testCase.id}`),
		).toContainText("Failed");
		await expect(
			page.getByTestId(`test-run-step-actual-${failed.step.id}`),
		).toContainText(failureEvidence);
		await expect(
			page.getByTestId(`test-run-step-notes-${failed.step.id}`),
		).toContainText(failureNotes);
		await expect(
			page.getByTestId(`test-run-step-item-${failed.step.id}`),
		).toHaveAttribute(
			"href",
			`/workspaces/${workspace.id}/items/${linkedIssue.id}`,
		);
		await page.getByTestId("test-run-detail-back").click();
		await expect(page).toHaveURL(
			new RegExp(`/workspaces/${workspace.id}/tests/reports$`),
		);
		await page.getByTestId(`test-report-failure-run-${run.id}`).click();

		await page.context().addInitScript(() => {
			// @ts-expect-error neutralize the native print dialog in headless runs
			window.print = () => {};
		});
		const summaryPopupPromise = page.waitForEvent("popup");
		await page.getByTestId("test-run-export-results").click();
		const summaryPopup = await summaryPopupPromise;
		await expect
			.poll(() => summaryPopup.url())
			.toMatch(
				new RegExp(`/workspaces/${workspace.id}/tests/runs/${run.id}/print$`),
			);
		const summaryBody = summaryPopup.getByTestId("test-run-summary-print-body");
		await expect(summaryBody).toContainText(runName, { timeout: 15_000 });
		await expect(summaryBody).toContainText("Statistics");
		expect(
			await summaryBody.evaluate(
				(body) => body.querySelectorAll("table").length,
			),
		).toBe(2);
		await expect(
			summaryPopup.getByTestId("test-run-summary-print-button"),
		).toBeVisible();
		await summaryPopup.close();

		page.once("dialog", (dialog) => dialog.accept(rerunName));
		const rerunResponsePromise = waitForResponse(
			page,
			"POST",
			`/api/v2/workspaces/${workspace.id}/test-runs`,
		);
		await page.getByTestId("test-run-rerun").click();
		const rerun = await responseBody<Identified>(
			await rerunResponsePromise,
			"create rerun",
		);
		await expect(execution).toHaveAttribute("data-run-id", String(rerun.id));
		await page.getByTestId("test-execution-back").click();
		// The rerun is independent: the original run stays listed as Completed
		// while the new run is in progress.
		await expect(page.getByTestId(`test-run-row-${run.id}`)).toContainText(
			"Completed",
		);
		await expect(page.getByTestId(`test-run-row-${rerun.id}`)).toBeVisible();

		await page.goto(`/workspaces/${workspace.id}/tests/sets`);
		await expect(page.getByTestId(`test-set-row-${testSet.id}`)).toContainText(
			"2 total",
		);
	});

	// WI-390 (GH #134): selecting an assignee used to make run creation fail
	// silently for active users without an explicit workspace role.
	test("creates a run with an assignee from the Test Runs section", async ({
		page,
		getCtx,
	}) => {
		const ctx = await getCtx("admin");
		const ws = ctx.workspaceId;
		const stamp = `${Date.now()}`;

		const caseResp = await ctx.request.post(
			`/api/v2/workspaces/${ws}/test-cases`,
			{
				headers: SEC_FETCH,
				data: {
					title: `E2E TC assignee ${stamp}`,
					preconditions: "",
					priority: "medium",
					status: "active",
					estimated_duration: 0,
				},
			},
		);
		expect(caseResp.ok()).toBeTruthy();
		const testCase = (await caseResp.json()).data;

		const setResp = await ctx.request.post(
			`/api/v2/workspaces/${ws}/test-plans`,
			{
				headers: SEC_FETCH,
				data: { name: `E2E TS assignee ${stamp}`, description: "" },
			},
		);
		expect(setResp.ok()).toBeTruthy();
		const testSet = (await setResp.json()).data;

		const linkResp = await ctx.request.post(
			`/api/v2/workspaces/${ws}/test-plans/${testSet.id}/test-cases`,
			{ headers: SEC_FETCH, data: { test_case_id: testCase.id } },
		);
		expect(linkResp.ok()).toBeTruthy();

		const assigneeData = generateUser("wi390");
		const assignee = await createUserViaAPI(ctx.request, assigneeData);

		await page.goto(`/workspaces/${ws}/tests/runs`);
		await page.getByTestId("create-test-run-button").click();
		await page.locator("#set-select").click();
		await page.locator(`#set-select-option-${testSet.id}`).click();
		await page.locator("#run-name").fill(`E2E UI Run ${stamp}`);

		await page.getByTestId("user-picker-trigger").click();
		await page.getByTestId("user-picker-search").fill(assigneeData.username);
		await page.getByTestId(`user-picker-option-${assignee.id}`).click();

		const runResponsePromise = waitForResponse(
			page,
			"POST",
			`/api/v2/workspaces/${ws}/test-runs`,
		);
		await page.getByTestId("create-run-submit").click();
		const run = await responseBody<Identified>(
			await runResponsePromise,
			"create assigned test run",
		);

		await expect(page.getByTestId("create-run-submit")).toBeHidden();
		await expect(page.getByTestId(`test-run-row-${run.id}`)).toContainText(
			assigneeData.last_name,
		);
	});
});
