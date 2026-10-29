import { expect, test } from "../fixtures/context-path";

/**
 * Issue 373 — a saved connection's token must be replaceable without deleting
 * the connection and its import history.
 *
 * The spec pins the management-page flow: open the edit modal for a saved
 * connection, submit a new token, and confirm the PUT payload plus the
 * refreshed row. It mocks the connections endpoints because seeding a real
 * Jira tenant is out of scope; the credential verification itself is covered
 * by the Go handler tests.
 */

test("saved Jira connection token can be replaced from the management page", async ({
	page,
}) => {
	let connections = [
		{
			id: "conn-rotate",
			instance_url: "https://example.atlassian.net",
			email: "old@example.com",
			instance_name: "Rotate me",
			deployment_type: "cloud",
			last_used_at: "2026-05-17T12:00:00Z",
		},
	];

	await page.route("**/api/admin/jira-import/connections", async (route) => {
		if (route.request().method() !== "GET") return route.fallback();
		await route.fulfill({
			status: 200,
			contentType: "application/json",
			body: JSON.stringify(connections),
		});
	});
	await page.route("**/api/admin/jira-import/jobs", async (route) => {
		if (route.request().method() !== "GET") return route.fallback();
		await route.fulfill({
			status: 200,
			contentType: "application/json",
			body: "[]",
		});
	});

	const updateRequests: Array<Record<string, unknown>> = [];
	await page.route(
		"**/api/admin/jira-import/connections/conn-rotate",
		async (route) => {
			const payload = route.request().postDataJSON() as Record<string, unknown>;
			updateRequests.push(payload);
			connections = [
				{
					...connections[0],
					email: "new@example.com",
					instance_name: "Rotated Jira",
				},
			];
			await route.fulfill({
				status: 200,
				contentType: "application/json",
				body: JSON.stringify(connections[0]),
			});
		},
	);

	await page.goto("/admin/system-import");
	const row = page.getByTestId("jira-import-connection-conn-rotate");
	await expect(row).toBeVisible();
	await expect(row).toContainText("Rotate me");

	await row.getByTestId("jira-import-connection-actions-conn-rotate").click();
	await page.getByTestId("jira-import-connection-edit-conn-rotate").click();

	const emailInput = page.getByTestId("jira-import-edit-email");
	const tokenInput = page.getByTestId("jira-import-edit-token");
	await expect(emailInput).toBeVisible();
	await expect(tokenInput).toBeVisible();
	await emailInput.fill("new@example.com");
	await tokenInput.fill("rotated-token");

	await page.getByTestId("dialog-confirm").click();

	const toast = page.getByTestId("toast").first();
	await expect(toast).toBeVisible({ timeout: 5_000 });
	await expect(toast).toContainText(/updated/i);

	expect(updateRequests).toHaveLength(1);
	expect(updateRequests[0]).toMatchObject({
		email: "new@example.com",
		api_token: "rotated-token",
	});

	// The list reloads, so the row reflects the instance name returned by the
	// re-verification rather than the stale cached value.
	await expect(row).toContainText("Rotated Jira");
});

test("removing a saved Jira connection keeps its import history", async ({
	page,
}) => {
	const connection = {
		id: "conn-remove",
		instance_url: "https://example.atlassian.net",
		email: "admin@example.com",
		instance_name: "Remove me",
		deployment_type: "cloud",
		last_used_at: "2026-05-17T12:00:00Z",
	};

	await page.route("**/api/admin/jira-import/connections", async (route) => {
		if (route.request().method() !== "GET") return route.fallback();
		await route.fulfill({
			status: 200,
			contentType: "application/json",
			body: JSON.stringify([connection]),
		});
	});
	await page.route("**/api/admin/jira-import/jobs", async (route) => {
		if (route.request().method() !== "GET") return route.fallback();
		await route.fulfill({
			status: 200,
			contentType: "application/json",
			body: JSON.stringify([
				{
					id: "job-keep",
					connection_id: "conn-remove",
					instance_name: "Removed connection instance",
					instance_url: "https://example.atlassian.net",
					status: "completed",
					phase: "completed",
					scope: "work_items",
					project_keys: ["APP"],
					imported_workspace_count: 1,
					imported_item_count: 2,
					created_at: "2026-05-17T11:00:00Z",
					started_at: "2026-05-17T11:00:00Z",
					completed_at: "2026-05-17T11:05:00Z",
					progress: {},
				},
			]),
		});
	});

	const deleteMethods: string[] = [];
	await page.route(
		"**/api/admin/jira-import/connections/conn-remove",
		async (route) => {
			deleteMethods.push(route.request().method());
			await route.fulfill({ status: 204, body: "" });
		},
	);

	await page.goto("/admin/system-import");
	const row = page.getByTestId("jira-import-connection-conn-remove");
	await expect(row).toBeVisible();
	const historyRow = page.getByTestId("jira-import-history-row");
	await expect(historyRow).toContainText("Removed connection instance");

	await row.getByTestId("jira-import-connection-actions-conn-remove").click();
	await page.getByTestId("jira-import-connection-delete-conn-remove").click();
	await page.getByTestId("dialog-confirm").click();

	const toast = page.getByTestId("toast").first();
	await expect(toast).toBeVisible({ timeout: 5_000 });
	await expect(toast).toContainText(/removed/i);

	expect(deleteMethods).toEqual(["DELETE"]);
	await expect(
		page.getByTestId("jira-import-connection-conn-remove"),
	).toHaveCount(0);
	// The history row survives the connection removal: the UI path is a soft
	// remove, not a hard delete of the imported data.
	await expect(historyRow).toContainText("Removed connection instance");
});

test("import history row can be cleared without deleting imported data", async ({
	page,
}) => {
	let jobs = [
		{
			id: "job-hide",
			connection_id: "conn-hide",
			instance_name: "Hide me",
			instance_url: "https://example.atlassian.net",
			status: "completed",
			phase: "completed",
			scope: "work_items",
			project_keys: ["APP"],
			imported_workspace_count: 1,
			imported_item_count: 2,
			created_at: "2026-05-17T11:00:00Z",
			started_at: "2026-05-17T11:00:00Z",
			completed_at: "2026-05-17T11:05:00Z",
			progress: {},
		},
	];

	await page.route("**/api/admin/jira-import/connections", async (route) => {
		if (route.request().method() !== "GET") return route.fallback();
		await route.fulfill({
			status: 200,
			contentType: "application/json",
			body: "[]",
		});
	});
	await page.route("**/api/admin/jira-import/jobs", async (route) => {
		if (route.request().method() !== "GET") return route.fallback();
		await route.fulfill({
			status: 200,
			contentType: "application/json",
			body: JSON.stringify(jobs),
		});
	});

	const hideMethods: string[] = [];
	const dataDeleteCalls: string[] = [];
	await page.route("**/api/admin/jira-import/jobs/job-hide", async (route) => {
		hideMethods.push(route.request().method());
		jobs = [];
		await route.fulfill({ status: 204, body: "" });
	});
	await page.route(
		"**/api/admin/jira-import/jobs/job-hide/data",
		async (route) => {
			dataDeleteCalls.push(route.request().method());
			await route.fulfill({ status: 200, contentType: "application/json", body: "{}" });
		},
	);

	await page.goto("/admin/system-import");
	const historyRow = page.getByTestId("jira-import-history-row");
	await expect(historyRow).toContainText("Hide me");

	await page.getByTestId("jira-import-history-actions-job-hide").click();
	await page.getByTestId("jira-import-history-hide-job-hide").click();

	const toast = page.getByTestId("toast").first();
	await expect(toast).toBeVisible({ timeout: 5_000 });
	await expect(toast).toContainText(/removed from history/i);

	expect(hideMethods).toEqual(["DELETE"]);
	expect(dataDeleteCalls).toEqual([]);
	await expect(page.getByTestId("jira-import-history-row")).toHaveCount(0);
});
