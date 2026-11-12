import { expect, test } from "../fixtures/context-path";
import {
	createCustomFieldViaAPI,
	createItemViaAPI,
	createWorkspaceViaAPI,
	deleteCustomFieldViaAPI,
} from "../fixtures/api-helpers";
import { generateWorkspace } from "../fixtures/test-data";

const SEC_FETCH = { "Sec-Fetch-Site": "same-origin" };

/**
 * Issue #276: asset custom fields rendered empty cells in the list view even
 * though the value was set and visible in the item detail. The list cell must
 * resolve the stored value — both the {id, title, asset_tag} object written
 * by the picker and a bare asset id — to the asset's display label.
 */
test.describe("Asset custom field in list view", () => {
	test("assigned asset renders its label in the list column", async ({
		page,
		request,
	}) => {
		const suffix = `${Date.now()}-${Math.floor(Math.random() * 10000)}`;

		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace(`asset-cf-list-${suffix}`.slice(0, 60)),
		);

		// Asset set → type → one asset to reference.
		const setResponse = await request.post("/api/v2/asset-sets", {
			headers: SEC_FETCH,
			data: { name: `Asset set ${suffix}`, description: "e2e list asset cf" },
		});
		expect(
			setResponse.ok(),
			`create asset set: ${setResponse.status()} ${await setResponse.text()}`,
		).toBeTruthy();
		const set = (await setResponse.json()).data ?? (await setResponse.json());

		const typeResponse = await request.post(`/api/v2/asset-sets/${set.id}/types`, {
			headers: SEC_FETCH,
			data: { name: "Component", description: "type", icon: "Box", color: "#1f6feb" },
		});
		expect(typeResponse.ok()).toBeTruthy();
		const assetType = (await typeResponse.json()).data ?? (await typeResponse.json());

		const assetResponse = await request.post(`/api/v2/asset-sets/${set.id}/assets`, {
			headers: SEC_FETCH,
			data: { title: `Bearing ${suffix}`, asset_type_id: assetType.id },
		});
		expect(assetResponse.ok()).toBeTruthy();
		const asset = (await assetResponse.json()).data ?? (await assetResponse.json());

		const field = await createCustomFieldViaAPI(request, {
			name: `Component ${suffix}`,
			field_type: "asset",
			required: false,
			options: JSON.stringify({ asset_set_id: set.id }),
		});

		// Workspace-level list configuration carrying the asset column.
		const configResponse = await request.put(
			`/api/v2/workspaces/${workspace.id}/board-configuration`,
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
							field_identifier: String(field.id),
							field_type: "custom",
							display_order: 2,
							width: 2,
						},
					],
				},
			},
		);
		expect(
			configResponse.ok(),
			`save list config: ${configResponse.status()} ${await configResponse.text()}`,
		).toBeTruthy();

		// Two items covering both persisted shapes: the picker's object value
		// and a bare asset id (API/import path).
		const objectItem = await createItemViaAPI(request, workspace.id, {
			title: `Asset object ${suffix}`,
			custom_field_values: {
				[String(field.id)]: { id: asset.id, title: `Bearing ${suffix}`, asset_tag: "" },
			},
		});
		const idItem = await createItemViaAPI(request, workspace.id, {
			title: `Asset id ${suffix}`,
			custom_field_values: { [String(field.id)]: asset.id },
		});

		try {
			await page.goto(`/workspaces/${workspace.id}/list`);

			const objectCell = page.getByTestId(
				`list-custom-field-${field.id}-${objectItem.id}`,
			);
			await expect(objectCell).toBeVisible({ timeout: 10000 });
			await expect(objectCell).toContainText(`Bearing ${suffix}`);

			// The bare id resolves through the shared asset display cache.
			const idCell = page.getByTestId(`list-custom-field-${field.id}-${idItem.id}`);
			await expect(idCell).toContainText(`Bearing ${suffix}`, { timeout: 10000 });

			// The cells are click-to-edit displays, not unresolved picker
			// placeholders like the ones reported in the issue.
			await expect(page.getByTestId("list-view")).not.toContainText("Select asset");
		} finally {
			await request
				.delete(`/api/v2/custom-fields/${field.id}`, { headers: SEC_FETCH })
				.catch(() => {});
		}
	});
});
