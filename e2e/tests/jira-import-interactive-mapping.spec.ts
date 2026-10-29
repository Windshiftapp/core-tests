import { expect, test } from "../fixtures/context-path";

// Interactive mapping is a user workflow: the operator must be able to land a
// Jira project in an existing workspace and reuse existing item types, statuses,
// and custom fields. The mocked analyze payload advertises those targets and the
// test asserts the resulting start payload, not just that controls render.
test("Jira import maps onto existing workspace, item type, status, and custom field", async ({
	page,
}) => {
	await page.route("**/api/admin/jira-import/connections", async (route) => {
		if (route.request().method() !== "GET") return route.fallback();
		await route.fulfill({
			status: 200,
			contentType: "application/json",
			body: JSON.stringify([
				{
					id: "connection-mapping",
					instance_url: "https://example.atlassian.net",
					email: "admin@example.com",
					instance_name: "Jira mapping",
					deployment_type: "cloud",
					last_used_at: "2026-10-06T06:00:00Z",
				},
			]),
		});
	});
	await page.route("**/api/admin/jira-import/jobs", async (route) => {
		if (route.request().method() !== "GET") return route.fallback();
		await route.fulfill({ status: 200, contentType: "application/json", body: "[]" });
	});
	await page.route("**/api/admin/jira-import/projects?*", async (route) => {
		await route.fulfill({
			status: 200,
			contentType: "application/json",
			body: JSON.stringify([
				{
					id: "10000",
					key: "APP",
					name: "Application",
					project_type: "software",
					is_team_managed: true,
				},
			]),
		});
	});
	await page.route("**/api/admin/jira-import/projects/counts", async (route) => {
		await route.fulfill({
			status: 200,
			contentType: "application/json",
			body: JSON.stringify({ APP: 2 }),
		});
	});
	await page.route("**/api/admin/jira-import/analyze", async (route) => {
		await route.fulfill({
			status: 200,
			contentType: "application/json",
			body: JSON.stringify({
				projects: [
					{
						key: "APP",
						name: "Application",
						issue_count: 2,
						suggested_workspace_key: "APP",
						is_team_managed: true,
					},
				],
				issue_types: [
					{ id: "10001", name: "Story", subtask: false, hierarchy_level: 0 },
				],
				statuses: [
					{
						id: "1",
						name: "In Progress",
						category_key: "indeterminate",
						category_name: "In Progress",
						color: "#0052cc",
					},
				],
				custom_fields: [
					{
						jira_field_id: "customfield_1",
						jira_field_name: "Environment",
						jira_field_type: "string",
						windshift_field_type: "text",
						can_map: true,
					},
				],
				mapping_targets: {
					workspaces: [{ id: 7, key: "EXST", name: "Existing target" }],
					item_types: [{ id: 11, name: "Existing type", hierarchy_level: 0 }],
					statuses: [
						{
							id: 12,
							name: "Existing status",
							category_id: 1,
							category_name: "To Do",
							color: "#cccccc",
						},
					],
					custom_fields: [{ id: 13, name: "Existing field", field_type: "text" }],
				},
				versions: [],
				users: [],
				asset_schemas: [],
				service_management_projects: [],
				total_issues: 2,
				total_assets: 0,
				xray: {
					detection_status: "not_detected",
					total_tests: 0,
					projects: [],
					test_issue_type_ids: [],
				},
			}),
		});
	});

	const importRequests: Array<Record<string, unknown>> = [];
	await page.route("**/api/admin/jira-import/start", async (route) => {
		importRequests.push(route.request().postDataJSON() as Record<string, unknown>);
		await route.fulfill({
			status: 202,
			contentType: "application/json",
			body: JSON.stringify({ job_id: "job-mapping" }),
		});
	});
	await page.route("**/api/admin/jira-import/jobs/job-mapping", async (route) => {
		await route.fulfill({
			status: 200,
			contentType: "application/json",
			body: JSON.stringify({
				status: "completed",
				phase: "completed",
				progress: {
					total_issues: 2,
					imported_issues: 2,
					total_comments: 0,
					imported_comments: 0,
					total_attachments: 0,
					imported_attachments: 0,
				},
			}),
		});
	});

	await page.goto("/admin/system-import");
	const connection = page.getByTestId("jira-import-connection-connection-mapping");
	await expect(connection).toBeVisible();
	await connection.getByTestId("jira-import-connection-start").click();

	await page.getByTestId("jira-import-next").click();
	await page.getByTestId("jira-import-project-APP").click();
	await page.getByTestId("jira-import-next").click();

	await expect(page.getByTestId("jira-import-step-mapping")).toBeVisible();

	await page.locator("#jira-import-workspace-target-APP").click();
	await page.locator("#jira-import-workspace-target-APP-option-existing-7").click();
	await expect(page.getByTestId("jira-import-workspace-name-APP")).toHaveCount(0);
	await expect(
		page.getByTestId("jira-import-workspace-mapping-APP"),
	).toContainText("Existing target");

	await page.locator("#jira-import-issue-type-target-10001").click();
	await page.locator("#jira-import-issue-type-target-10001-option-existing-11").click();

	await page.locator("#jira-import-status-target-1").click();
	await page.locator("#jira-import-status-target-1-option-existing-12").click();

	await page.locator("#jira-import-field-target-select-customfield_1").click();
	await page.locator("#jira-import-field-target-select-customfield_1-option-map-13").click();

	await page.getByTestId("jira-import-next").click();
	await expect(page.getByTestId("jira-import-step-preview")).toBeVisible();
	await page.getByTestId("jira-import-next").click();
	await expect(page.getByTestId("jira-import-complete")).toBeVisible();

	expect(importRequests).toHaveLength(1);
	expect(importRequests[0].mappings).toMatchObject({
		workspaces: [{ jiraKey: "APP", createNew: false, windshiftId: 7 }],
		issueTypes: [{ jiraName: "Story", createNew: false, windshiftId: 11 }],
		statuses: [{ jiraName: "In Progress", createNew: false, windshiftId: 12 }],
		customFields: [{ jiraId: "customfield_1", action: "map", windshiftId: 13 }],
	});
});
