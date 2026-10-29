import {
	createCustomFieldViaAPI,
	createItemViaAPI,
	createUserViaAPI,
	createWorkspaceViaAPI,
} from "../fixtures/api-helpers";
import { createActionViaAPI } from "../fixtures/action-editor-helpers";
import { expect, test } from "../fixtures/context-path";
import { shot } from "../helpers/screenshot";

const SEC_FETCH = { "Sec-Fetch-Site": "same-origin" };

interface NamedRow {
	id: number;
	name: string;
}

/**
 * End-to-end journey for the create_asset automation node.
 *
 * Setup (API) builds an "Application" asset type with text/user/select/boolean
 * custom fields and a manual action whose create_asset node maps those fields
 * from string literals. The browser then:
 *   - renders the persisted node config in the action editor,
 *   - executes the action against a real work item,
 *   - shows the execution log,
 *   - and proves the created asset carries every coerced custom field.
 */
test("create_asset action builds an application with typed custom fields", async ({
	request,
	page,
}, testInfo) => {
	test.setTimeout(120_000);
	const stamp = `${Date.now()}-${testInfo.workerIndex}-${testInfo.repeatEachIndex}`;
	const assetTitle = `Application ${stamp}`;
	const nameValue = `Payroll Portal ${stamp}`;
	const noDataTitle = `Application without personal data ${stamp}`;
	const noDataName = `Static Site ${stamp}`;
	const itemTitle = `Register application ${stamp}`;

	// ── Setup through the API ────────────────────────────────────────────
	const workspace = await createWorkspaceViaAPI(
		request,
		{
			name: `Asset app journey ${stamp}`,
			key: `AAJ${stamp.replace(/\D/g, "").slice(-6)}`,
			description: "create_asset e2e",
		},
	);
	const item = await createItemViaAPI(request, workspace.id, {
		title: itemTitle,
	});
	const owner = await createUserViaAPI(request, {
		email: `app-owner-${stamp}@example.test`,
		username: `app-owner-${stamp}`,
		first_name: "Application",
		last_name: `Owner${stamp}`,
		password_hash: "TestPass123!",
	});
	const ownerName = `Application Owner${stamp}`;

	const setResponse = await request.post("/api/v2/asset-sets", {
		headers: SEC_FETCH,
		data: {
			name: `Applications ${stamp}`,
			description: "create_asset e2e",
		},
	});
	expect(setResponse.status(), await setResponse.text()).toBe(201);
	const set = (await setResponse.json()).data as NamedRow;

	const typeResponse = await request.post(
		`/api/v2/asset-sets/${set.id}/types`,
		{
			headers: SEC_FETCH,
			data: { name: `Application ${stamp}` },
		},
	);
	expect(typeResponse.status(), await typeResponse.text()).toBe(201);
	const assetType = (await typeResponse.json()).data as NamedRow;

	const statusesResponse = await request.get(
		`/api/v2/asset-sets/${set.id}/statuses`,
		{ headers: SEC_FETCH },
	);
	expect(statusesResponse.ok()).toBeTruthy();
	const statuses = (await statusesResponse.json()).data as Array<
		NamedRow & { is_default: boolean }
	>;
	const defaultStatus =
		statuses.find((status) => status.is_default) ?? statuses[0];
	expect(defaultStatus, "asset set seeds a default status").toBeDefined();

	const nameField = await createCustomFieldViaAPI(request, {
		name: `Name ${stamp}`,
		field_type: "text",
	});
	const ownerField = await createCustomFieldViaAPI(request, {
		name: `Owner ${stamp}`,
		field_type: "user",
	});
	const typeField = await createCustomFieldViaAPI(request, {
		name: `Type ${stamp}`,
		field_type: "select",
		options: JSON.stringify({
			next_id: 4,
			items: [
				{ id: 1, label: "Desktop" },
				{ id: 2, label: "Web" },
				{ id: 3, label: "Mobile" },
			],
		}),
	});
	const personalDataField = await createCustomFieldViaAPI(request, {
		name: `Stores personal data ${stamp}`,
		field_type: "boolean",
	});

	const attachResponse = await request.put(
		`/api/v2/asset-types/${assetType.id}/fields`,
		{
			headers: SEC_FETCH,
			data: {
				fields: [
					{
						custom_field_id: nameField.id,
						is_required: true,
						display_order: 0,
					},
					{
						custom_field_id: ownerField.id,
						is_required: false,
						display_order: 1,
					},
					{
						custom_field_id: typeField.id,
						is_required: false,
						display_order: 2,
					},
					{
						custom_field_id: personalDataField.id,
						is_required: false,
						display_order: 3,
					},
				],
			},
		},
	);
	expect(attachResponse.status(), await attachResponse.text()).toBe(200);

	// Both custom-field values are string literals, so the create pipeline must
	// coerce "user"/"true"/"false" to the declared types.
	const createAssetNode = (
		id: number,
		title: string,
		name: string,
		personalData: string,
		positionY: number,
	) => ({
		id,
		node_type: "create_asset",
		node_config: JSON.stringify({
			asset_set_id: set.id,
			asset_type_id: assetType.id,
			status_id: defaultStatus!.id,
			category_id: null,
			title,
			description: "",
			asset_tag: "",
			field_mappings: [
				{
					source_type: "literal",
					source_value: name,
					target_field_id: String(nameField.id),
				},
				{
					source_type: "literal",
					source_value: String(owner.id),
					target_field_id: String(ownerField.id),
				},
				{
					source_type: "literal",
					source_value: "2",
					target_field_id: String(typeField.id),
				},
				{
					source_type: "literal",
					source_value: personalData,
					target_field_id: String(personalDataField.id),
				},
			],
		}),
		position_x: 240,
		position_y: positionY,
	});

	const action = await createActionViaAPI(request, workspace.id, {
		name: `Create application ${stamp}`,
		description: "create_asset e2e",
		trigger_type: "manual",
		nodes: [
			{
				id: 1,
				node_type: "trigger",
				node_config: "{}",
				position_x: 0,
				position_y: 0,
			},
			createAssetNode(2, assetTitle, nameValue, "true", 0),
			createAssetNode(3, noDataTitle, noDataName, "false", 160),
		],
		edges: [
			{ source_node_id: 1, target_node_id: 2, edge_type: "default" },
			{ source_node_id: 2, target_node_id: 3, edge_type: "default" },
		],
	});

	// ── The action editor renders the persisted node config ──────────────
	const catalogReady = page.waitForResponse(
		(response) =>
			response.url().includes(`/api/v2/workspaces/${workspace.id}/action-catalog`) &&
			response.ok(),
	);
	await page.goto(`/workspaces/${workspace.id}/actions/${action.id}`);
	await expect(page.getByTestId("action-editor-canvas")).toBeVisible();
	await catalogReady;
	await page.getByTestId("action-node-create_asset").first().click();
	await expect(page.locator("#asset-title")).toHaveValue(assetTitle);
	await expect(page.locator("#asset-type")).toContainText(assetType.name);
	await expect(page.locator("#asset-status")).toContainText(defaultStatus!.name);
	await shot(page, "01-action-editor-create-asset");

	// ── Execute the action from the actions list ─────────────────────────
	await page.goto(`/workspaces/${workspace.id}/actions`);
	const actionCard = page.getByTestId(`action-card-${action.id}`);
	await expect(actionCard).toBeVisible();
	await actionCard.getByTestId("action-test").click();
	await page.getByTestId("item-picker-trigger").click();
	const searchInput = page.getByTestId("action-test-item-search");
	await expect(searchInput).toBeVisible();
	await searchInput.fill(itemTitle);
	await page.getByTestId(`action-test-item-${item.id}`).click();
	await shot(page, "02-action-test-item-picker");
	await page.getByTestId("action-test-execute").click();
	await expect(page.getByTestId("action-test-execute")).toHaveCount(0);

	await actionCard.getByTestId("action-view-logs").click();
	const logs = page.getByTestId("action-logs");
	await expect(logs).toBeVisible();
	await expect(logs).toContainText("Completed");
	await shot(page, "03-action-logs-completed");

	// ── Both created assets expose every typed custom field ──────────────
	const assetsResponse = await request.get(
		`/api/v2/asset-sets/${set.id}/assets`,
		{ headers: SEC_FETCH },
	);
	expect(assetsResponse.ok()).toBeTruthy();
	const createdAssets = (await assetsResponse.json()).data as Array<{
		id: number;
		title: string;
	}>;
	const withPersonalData = createdAssets.find(
		(asset) => asset.title === assetTitle,
	);
	const withoutPersonalData = createdAssets.find(
		(asset) => asset.title === noDataTitle,
	);
	expect(withPersonalData, `action created asset ${assetTitle}`).toBeDefined();
	expect(
		withoutPersonalData,
		`action created asset ${noDataTitle}`,
	).toBeDefined();

	await page.goto("/assets");
	await page.locator("#asset-set-select").click();
	await page.locator(`#asset-set-select-option-${set.id}`).click();
	await expect(
		page.getByTestId(`asset-title-${withPersonalData!.id}`),
	).toBeVisible();
	await expect(
		page.getByTestId(`asset-title-${withoutPersonalData!.id}`),
	).toBeVisible();
	await shot(page, "04-assets-list");

	// true → "Yes"
	await page.getByTestId(`asset-title-${withPersonalData!.id}`).click();
	const detailPane = page.getByTestId("asset-detail-pane");
	await expect(detailPane).toBeVisible();
	await expect(detailPane).toContainText(nameValue);
	await expect(detailPane).toContainText(ownerName);
	await expect(detailPane).toContainText("Web");
	await expect(detailPane).toContainText("Yes");
	await expect(detailPane).toContainText(defaultStatus!.name);
	await shot(page, "05-asset-detail-boolean-yes", { locator: detailPane });

	// false → "No"
	await page.getByTestId(`asset-title-${withoutPersonalData!.id}`).click();
	await expect(detailPane).toContainText(noDataName);
	await expect(detailPane).toContainText("No");
	await expect(detailPane).not.toContainText("Yes");
	await shot(page, "06-asset-detail-boolean-no", { locator: detailPane });

	// A fresh navigation re-fetches the asset, proving the coerced values were
	// persisted rather than only rendered from in-memory state.
	await page.goto("/assets");
	await page.locator("#asset-set-select").click();
	await page.locator(`#asset-set-select-option-${set.id}`).click();
	await page.getByTestId(`asset-title-${withoutPersonalData!.id}`).click();
	const reloadedPane = page.getByTestId("asset-detail-pane");
	await expect(reloadedPane).toContainText(noDataName);
	await expect(reloadedPane).toContainText("No");
	await expect(reloadedPane).not.toContainText("Yes");
	await shot(page, "07-asset-detail-false-after-reload", {
		locator: reloadedPane,
	});
});
