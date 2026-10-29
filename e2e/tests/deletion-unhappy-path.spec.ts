import { randomUUID } from "node:crypto";
import type { APIRequestContext } from "@playwright/test";
import {
	createCustomFieldViaAPI,
	createItemViaAPI,
	createIterationViaAPI,
	createMilestoneViaAPI,
	createWorkspaceViaAPI,
	deleteCustomFieldViaAPI,
	listIterationTypesViaAPI,
	listWorkspaceItemTypesViaAPI,
	updateItemViaAPI,
} from "../fixtures/api-helpers";
import { expect, test } from "../fixtures/errors";
import { generateWorkspace } from "../fixtures/test-data";

/**
 * Unhappy-path deletion coverage: an entity that other rows reference is
 * deleted out from under an open view. The app must degrade gracefully — no
 * pageerror/console.error (the errors fixture fails the test on either), the
 * view stays usable after a reload, and the reference is detached, ignored, or
 * the deletion is refused with a visible reason.
 */

const SEC_FETCH = { "Sec-Fetch-Site": "same-origin" };

function unique(label: string): string {
	return `${label}-${Date.now().toString(36)}-${randomUUID().slice(0, 6)}`;
}

async function createItemType(
	request: APIRequestContext,
	name: string,
): Promise<{ id: number; name: string }> {
	const response = await request.post("/api/v2/item-types", {
		headers: SEC_FETCH,
		data: {
			name,
			description: "E2E deletion resilience",
			is_default: false,
			icon: "Circle",
			color: "#2563eb",
			hierarchy_level: 3,
			sort_order: 250,
		},
	});
	expect(
		response.ok(),
		`create item type ${name}: ${response.status()} ${await response.text()}`,
	).toBeTruthy();
	return (await response.json()).data;
}

async function configureListColumns(
	request: APIRequestContext,
	workspaceId: number,
	customFieldId: number,
): Promise<void> {
	const response = await request.put(
		`/api/v2/workspaces/${workspaceId}/board-configuration`,
		{
			headers: SEC_FETCH,
			data: {
				columns: [],
				backlog_status_ids: [],
				list_columns: [
					{
						field_identifier: "key",
						field_type: "system",
						display_order: 0,
						width: 1,
					},
					{
						field_identifier: "title",
						field_type: "system",
						display_order: 1,
						width: 4,
					},
					{
						field_identifier: String(customFieldId),
						field_type: "custom",
						display_order: 2,
						width: 2,
					},
				],
			},
		},
	);
	expect(
		response.ok(),
		`save list columns: ${response.status()} ${await response.text()}`,
	).toBeTruthy();
}

test.describe("deleting a milestone referenced by an item", () => {
	test("item detail detaches the milestone and the board lane disappears", async ({
		page,
		request,
		allowConsoleError,
	}) => {
		test.setTimeout(90_000);
		// Rapid route changes abort the workspace-navigation collections fetch;
		// the browser surfaces that as a TypeError instead of AbortError.
		allowConsoleError(/Failed to load collections: TypeError: Failed to fetch/);
		const label = unique("milestone-delete");
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace(label),
		);
		const milestone = await createMilestoneViaAPI(request, {
			name: `Delete me ${label}`,
			workspace_id: workspace.id,
		});
		const item = await createItemViaAPI(request, workspace.id, {
			title: `Milestone item ${label}`,
		});
		await updateItemViaAPI(request, item.id, { milestone_ids: [milestone.id] });

		await page.goto(`/workspaces/${workspace.id}/items/${item.id}`);
		await expect(page.getByTestId("item-detail-ready")).toBeVisible({
			timeout: 15_000,
		});
		await expect(page.getByTestId("milestone-field")).toContainText(
			milestone.name,
		);

		await page.goto(`/workspaces/${workspace.id}/board`);
		await expect(page.getByTestId("board-view")).toBeVisible({
			timeout: 15_000,
		});
		await page.getByTestId("board-group-by-menu").click();
		await page.getByTestId("board-group-by-milestone").click();
		await expect(
			page.getByTestId(`board-lane-milestone-${milestone.id}`),
		).toBeVisible({ timeout: 15_000 });

		const deletion = await request.delete(
			`/api/v2/milestones/${milestone.id}`,
			{ headers: SEC_FETCH },
		);
		expect(
			deletion.status(),
			`delete milestone: ${deletion.status()} ${await deletion.text()}`,
		).toBe(204);

		await page.reload();
		await expect(page.getByTestId("board-view")).toBeVisible({
			timeout: 15_000,
		});
		await page.getByTestId("board-group-by-menu").click();
		await page.getByTestId("board-group-by-milestone").click();
		await expect(
			page.getByTestId(`board-lane-milestone-${milestone.id}`),
		).toHaveCount(0);

		await page.goto(`/workspaces/${workspace.id}/items/${item.id}`);
		await expect(page.getByTestId("item-detail-ready")).toBeVisible({
			timeout: 15_000,
		});
		await expect(page.getByTestId("milestone-field")).not.toContainText(
			milestone.name,
		);

		const detail = await request.get(`/api/v2/items/${item.id}`, {
			headers: SEC_FETCH,
		});
		expect(detail.ok()).toBeTruthy();
		const detailBody = await detail.json();
		expect(
			(detailBody.data.milestones ?? []).map(
				(entry: { id: number }) => entry.id,
			),
		).not.toContain(milestone.id);
	});
});

test.describe("deleting an iteration referenced by an item", () => {
	test("item detail drops the iteration and the view stays usable", async ({
		page,
		request,
	}) => {
		test.setTimeout(90_000);
		const label = unique("iteration-delete");
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace(label),
		);
		const types = await listIterationTypesViaAPI(request);
		expect(types.length).toBeGreaterThan(0);
		const start = new Date();
		const end = new Date();
		end.setDate(end.getDate() + 14);
		const iteration = await createIterationViaAPI(request, {
			name: `Delete me ${label}`,
			start_date: start.toISOString().split("T")[0],
			end_date: end.toISOString().split("T")[0],
			type_id: types[0].id,
			workspace_id: workspace.id,
		});
		const item = await createItemViaAPI(request, workspace.id, {
			title: `Iteration item ${label}`,
		});
		await updateItemViaAPI(request, item.id, { iteration_id: iteration.id });

		await page.goto(`/workspaces/${workspace.id}/items/${item.id}`);
		await expect(page.getByTestId("item-detail-ready")).toBeVisible({
			timeout: 15_000,
		});
		await expect(page.getByTestId("iteration-field")).toContainText(
			iteration.name,
		);

		const deletion = await request.delete(
			`/api/v2/iterations/${iteration.id}`,
			{ headers: SEC_FETCH },
		);
		expect(
			deletion.status(),
			`delete iteration: ${deletion.status()} ${await deletion.text()}`,
		).toBe(204);

		await page.reload();
		await expect(page.getByTestId("item-detail-ready")).toBeVisible({
			timeout: 15_000,
		});
		await expect(page.getByTestId("iteration-field")).not.toContainText(
			iteration.name,
		);

		await page.goto(`/workspaces/${workspace.id}/board`);
		await expect(page.getByTestId("board-view")).toBeVisible({
			timeout: 15_000,
		});
		await expect(page.getByTestId(`board-item-${item.id}`)).toBeVisible({
			timeout: 15_000,
		});
	});
});

test.describe("custom field deletion", () => {
	test("removing a select option leaves the list row and cell usable", async ({
		page,
		request,
	}) => {
		test.setTimeout(90_000);
		const label = unique("option-delete");
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace(label),
		);
		const field = await createCustomFieldViaAPI(request, {
			name: `Select ${label}`,
			field_type: "select",
			options: JSON.stringify({
				next_id: 4,
				items: [
					{ id: 1, label: "Low" },
					{ id: 2, label: "Medium" },
					{ id: 3, label: "High" },
				],
			}),
			required: false,
		});

		try {
			await configureListColumns(request, workspace.id, field.id);
			const item = await createItemViaAPI(request, workspace.id, {
				title: `Option item ${label}`,
				custom_field_values: { [String(field.id)]: 2 },
			});

			await page.goto(`/workspaces/${workspace.id}/list`);
			await expect(page.getByTestId("list-view")).toBeVisible({
				timeout: 15_000,
			});
			const cell = page.getByTestId(`list-custom-field-${field.id}-${item.id}`);
			await expect(cell).toBeVisible({ timeout: 15_000 });
			await expect(cell).toContainText("Medium");

			const patch = await request.patch(`/api/v2/custom-fields/${field.id}`, {
				headers: {
					...SEC_FETCH,
					"Content-Type": "application/merge-patch+json",
				},
				data: {
					options: JSON.stringify({
						next_id: 4,
						items: [
							{ id: 1, label: "Low" },
							{ id: 3, label: "High" },
						],
					}),
				},
			});
			expect(
				patch.ok(),
				`remove option: ${patch.status()} ${await patch.text()}`,
			).toBeTruthy();

			await page.reload();
			await expect(page.getByTestId("list-view")).toBeVisible({
				timeout: 15_000,
			});
			await expect(
				page.getByTestId(`workspace-item-row-${item.id}`),
			).toBeVisible({ timeout: 15_000 });
			const reloadedCell = page.getByTestId(
				`list-custom-field-${field.id}-${item.id}`,
			);
			await expect(reloadedCell).toBeVisible({ timeout: 15_000 });
			// The unknown option id must not resurrect the removed label.
			await expect(reloadedCell).not.toContainText("Medium");
		} finally {
			await deleteCustomFieldViaAPI(request, field.id).catch(() => {});
		}
	});

	test("a milestone custom field survives its referenced milestone being deleted", async ({
		page,
		request,
	}) => {
		test.setTimeout(90_000);
		const label = unique("milestone-field-delete");
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace(label),
		);
		const milestone = await createMilestoneViaAPI(request, {
			name: `Field milestone ${label}`,
			workspace_id: workspace.id,
		});
		const field = await createCustomFieldViaAPI(request, {
			name: `Milestone field ${label}`,
			field_type: "milestone",
			required: false,
		});

		try {
			await configureListColumns(request, workspace.id, field.id);
			const item = await createItemViaAPI(request, workspace.id, {
				title: `Milestone field item ${label}`,
				custom_field_values: { [String(field.id)]: milestone.id },
			});

			await page.goto(`/workspaces/${workspace.id}/list`);
			await expect(page.getByTestId("list-view")).toBeVisible({
				timeout: 15_000,
			});
			const cell = page.getByTestId(`list-custom-field-${field.id}-${item.id}`);
			await expect(cell).toContainText(milestone.name);

			const deletion = await request.delete(
				`/api/v2/milestones/${milestone.id}`,
				{ headers: SEC_FETCH },
			);
			expect(
				deletion.status(),
				`delete milestone: ${deletion.status()} ${await deletion.text()}`,
			).toBe(204);

			await page.reload();
			await expect(page.getByTestId("list-view")).toBeVisible({
				timeout: 15_000,
			});
			await expect(
				page.getByTestId(`workspace-item-row-${item.id}`),
			).toBeVisible({ timeout: 15_000 });
			// The renderer must not resolve a label for the deleted milestone.
			await expect(
				page.getByTestId(`list-custom-field-${field.id}-${item.id}`),
			).not.toContainText(milestone.name);
		} finally {
			await deleteCustomFieldViaAPI(request, field.id).catch(() => {});
		}
	});

	test("deleting an unused custom field drops its list column without breaking the view", async ({
		page,
		request,
	}) => {
		test.setTimeout(90_000);
		const label = unique("field-delete");
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace(label),
		);
		const field = await createCustomFieldViaAPI(request, {
			name: `Text ${label}`,
			field_type: "text",
			required: false,
		});
		await configureListColumns(request, workspace.id, field.id);
		const item = await createItemViaAPI(request, workspace.id, {
			title: `Field item ${label}`,
		});

		await page.goto(`/workspaces/${workspace.id}/list`);
		await expect(page.getByTestId("list-view")).toBeVisible({
			timeout: 15_000,
		});
		await expect(
			page.getByTestId(`list-custom-field-${field.id}-${item.id}`),
		).toBeVisible({ timeout: 15_000 });

		await deleteCustomFieldViaAPI(request, field.id);

		await page.reload();
		await expect(page.getByTestId("list-view")).toBeVisible({
			timeout: 15_000,
		});
		await expect(
			page.getByTestId(`workspace-item-row-${item.id}`),
		).toBeVisible({ timeout: 15_000 });
		await expect(
			page.getByTestId(`list-custom-field-${field.id}-${item.id}`),
		).toHaveCount(0);

		await page.goto(`/workspaces/${workspace.id}/items/${item.id}`);
		await expect(page.getByTestId("item-detail-ready")).toBeVisible({
			timeout: 15_000,
		});
		await expect(page.getByTestId(`item-custom-field-${field.id}`)).toHaveCount(
			0,
		);
	});

	test("deleting a custom field that still holds item values is refused", async ({
		page,
		request,
	}) => {
		test.setTimeout(90_000);
		const label = unique("field-in-use");
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace(label),
		);
		const field = await createCustomFieldViaAPI(request, {
			name: `Text ${label}`,
			field_type: "text",
			required: false,
		});

		await configureListColumns(request, workspace.id, field.id);
		const item = await createItemViaAPI(request, workspace.id, {
			title: `Field item ${label}`,
			custom_field_values: { [String(field.id)]: "keep me" },
		});

		await page.goto(`/workspaces/${workspace.id}/list`);
		await expect(page.getByTestId("list-view")).toBeVisible({
			timeout: 15_000,
		});
		await expect(
			page.getByTestId(`list-custom-field-${field.id}-${item.id}`),
		).toContainText("keep me");

		const deletion = await request.delete(
			`/api/admin/custom-fields/${field.id}`,
			{ headers: SEC_FETCH },
		);
		expect(
			deletion.status(),
			`delete in-use custom field: ${deletion.status()} ${await deletion.text()}`,
		).toBe(409);

		// The refusal leaves the field and its value untouched. The field stays
		// behind for the disposable database because a value-carrying field
		// cannot be deleted through the API.
		await page.reload();
		await expect(page.getByTestId("list-view")).toBeVisible({
			timeout: 15_000,
		});
		await expect(
			page.getByTestId(`list-custom-field-${field.id}-${item.id}`),
		).toContainText("keep me");
	});
});

test.describe("workspace deletion", () => {
	test("a deleted workspace stops rendering while the shell stays usable", async ({
		page,
		request,
		allowConsoleError,
	}) => {
		test.setTimeout(90_000);
		// The workspace is intentionally gone: the app logs the failed bootstrap
		// and gradient loads while it degrades to the shell, then stops.
		allowConsoleError(/\/api\/workspaces\/\d+\/bootstrap/);
		allowConsoleError(/WorkspaceDataStore: initialization failed/);
		allowConsoleError(/\/api\/workspaces\/\d+\/homepage\/layout/);
		allowConsoleError(/Failed to load workspace gradient/);
		allowConsoleError(/\/api\/v2\/workspaces\/\d+\/labels/);
		const label = unique("workspace-delete");
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace(label),
		);
		const item = await createItemViaAPI(request, workspace.id, {
			title: `Workspace item ${label}`,
		});

		await page.goto(`/workspaces/${workspace.id}/board`);
		await expect(page.getByTestId("board-view")).toBeVisible({
			timeout: 15_000,
		});
		await expect(page.getByTestId(`board-item-${item.id}`)).toBeVisible();

		const deletion = await request.delete(
			`/api/v2/workspaces/${workspace.id}`,
			{ headers: SEC_FETCH },
		);
		expect(
			deletion.status(),
			`delete workspace: ${deletion.status()} ${await deletion.text()}`,
		).toBe(204);

		await page.reload();
		// The app shell survives the missing workspace instead of blanking out.
		await expect(page.locator("#global-create-button")).toBeVisible({
			timeout: 15_000,
		});

		// The deleted workspace is gone from the switcher's search results.
		await page.goto("/workspaces");
		await page.getByTestId("workspaces-dropdown-trigger").click();
		const search = page.getByTestId("workspaces-search");
		await expect(search).toBeVisible({ timeout: 15_000 });
		await search.fill(workspace.name);
		await expect(page.getByTestId("workspace-dropdown-item")).toHaveCount(0);

		const list = await request.get("/api/v2/workspaces?page_size=1000", {
			headers: SEC_FETCH,
		});
		expect(list.ok()).toBeTruthy();
		const body = await list.json();
		expect(
			(body.data ?? []).map((entry: { id: number }) => entry.id),
		).not.toContain(workspace.id);
	});
});

test.describe("configuration set deletion", () => {
	test("deleting the bound configuration set keeps board and item detail usable", async ({
		page,
		request,
	}) => {
		test.setTimeout(90_000);
		const label = unique("config-delete");
		const itemType = await createItemType(request, `Config type ${label}`);
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace(label),
		);
		const configResponse = await request.post("/api/configuration-sets", {
			headers: SEC_FETCH,
			data: {
				name: `Config set ${label}`,
				description: "E2E deletion resilience",
				workspace_ids: [workspace.id],
				item_type_configs: [{ item_type_id: itemType.id }],
			},
		});
		expect(
			configResponse.ok(),
			`create configuration set: ${configResponse.status()} ${await configResponse.text()}`,
		).toBeTruthy();
		const configSet = await configResponse.json();
		const item = await createItemViaAPI(request, workspace.id, {
			title: `Config item ${label}`,
			item_type_id: itemType.id,
		});

		await page.goto(`/workspaces/${workspace.id}/items/${item.id}`);
		await expect(page.getByTestId("item-detail-ready")).toBeVisible({
			timeout: 15_000,
		});
		await expect(page.getByTestId("item-type-change-trigger")).toHaveAttribute(
			"data-item-type-id",
			String(itemType.id),
		);

		const deletion = await request.delete(
			`/api/configuration-sets/${configSet.id}`,
			{ headers: SEC_FETCH },
		);
		expect(
			deletion.ok(),
			`delete configuration set: ${deletion.status()} ${await deletion.text()}`,
		).toBeTruthy();

		await page.reload();
		await expect(page.getByTestId("item-detail-ready")).toBeVisible({
			timeout: 15_000,
		});
		await expect(page.getByTestId("item-type-change-trigger")).toHaveAttribute(
			"data-item-type-id",
			String(itemType.id),
		);

		await page.goto(`/workspaces/${workspace.id}/board`);
		await expect(page.getByTestId("board-view")).toBeVisible({
			timeout: 15_000,
		});
		await expect(page.getByTestId(`board-item-${item.id}`)).toBeVisible({
			timeout: 15_000,
		});
	});
});

test.describe("item type deletion", () => {
	test("deleting an in-use item type is refused with a visible reason", async ({
		page,
		request,
		allowConsoleError,
	}) => {
		test.setTimeout(90_000);
		// The intentional 409 is Chromium's failed-resource log for the refusal
		// the admin UI surfaces inline.
		allowConsoleError(/\/api\/v2\/item-types\/\d+:0:0.*409/);
		const label = unique("type-in-use");
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace(label),
		);
		const types = await listWorkspaceItemTypesViaAPI(request, workspace.id);
		expect(types.length).toBeGreaterThan(0);
		const itemType = types[0];
		await createItemViaAPI(request, workspace.id, {
			title: `Uses type ${label}`,
			item_type_id: itemType.id,
		});

		await page.goto("/admin/item-types");
		const row = page.getByTestId(`item-type-row-${itemType.id}`);
		await expect(row).toBeVisible({ timeout: 15_000 });
		await page.getByTestId(`item-type-actions-${itemType.id}`).click();
		await page.getByTestId("item-type-delete").click();
		await page.getByTestId("dialog-confirm").click();

		const error = page.getByTestId("item-type-error");
		await expect(error).toBeVisible({ timeout: 15_000 });
		await expect(error).toContainText(/used by/i);
		await expect(row).toBeVisible();
	});

	test("deleting an unused item type removes its admin row", async ({
		page,
		request,
	}) => {
		test.setTimeout(90_000);
		const label = unique("type-unused");
		const itemType = await createItemType(request, `Unused ${label}`);

		await page.goto("/admin/item-types");
		const row = page.getByTestId(`item-type-row-${itemType.id}`);
		await expect(row).toBeVisible({ timeout: 15_000 });
		await page.getByTestId(`item-type-actions-${itemType.id}`).click();
		await page.getByTestId("item-type-delete").click();
		await page.getByTestId("dialog-confirm").click();
		await expect(row).toHaveCount(0, { timeout: 15_000 });
	});
});
