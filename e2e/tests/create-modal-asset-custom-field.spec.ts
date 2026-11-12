import { expect, test } from "../fixtures/context-path";
import { ItemPage } from "../pages/item.page";
import { createCustomFieldViaAPI, createWorkspaceViaAPI } from "../fixtures/api-helpers";
import { generateWorkspace } from "../fixtures/test-data";

const SEC_FETCH = { "Sec-Fetch-Site": "same-origin" };

/**
 * Reproduction: setting an asset-type custom field ("Component") during item
 * creation must persist. The create form submits the picked asset as
 * {"id", "title", "asset_tag"}; the item must show it after creation.
 */
test.describe("Asset custom field on item create", () => {
	test("value picked in the create modal persists on the created item", async ({
		page,
		request,
	}) => {
		const suffix = `${Date.now()}-${Math.floor(Math.random() * 10000)}`;

		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace(`asset-cf-create-${suffix}`),
		);

		// Asset set + type + one asset to pick.
		const setResponse = await request.post("/api/v2/asset-sets", {
			headers: SEC_FETCH,
			data: { name: `Asset set ${suffix}`, description: "e2e create asset cf" },
		});
		expect(setResponse.ok(), `create asset set: ${setResponse.status()} ${await setResponse.text()}`).toBeTruthy();
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

		// Asset-type custom field.
		const field = await createCustomFieldViaAPI(request, {
			name: `Component ${suffix}`,
			field_type: "asset",
			required: false,
			options: JSON.stringify({ asset_set_id: set.id }),
		});

		// Screen with the field + config set wired to the workspace.
		const screenResponse = await request.post("/api/screens", {
			headers: SEC_FETCH,
			data: { name: `Asset CF screen ${suffix}`, description: "e2e" },
		});
		expect(screenResponse.ok()).toBeTruthy();
		const screen = await screenResponse.json();
		const fieldsResponse = await request.put(`/api/screens/${screen.id}/fields`, {
			headers: SEC_FETCH,
			data: [
				{
					field_type: "custom",
					field_identifier: String(field.id),
					display_order: 0,
					is_required: false,
					field_width: "full",
				},
			],
		});
		expect(fieldsResponse.ok()).toBeTruthy();

		const configSetResponse = await request.post("/api/configuration-sets", {
			headers: SEC_FETCH,
			data: {
				name: `Asset CF config ${suffix}`,
				description: "e2e",
				workspace_ids: [workspace.id],
				create_screen_id: screen.id,
				edit_screen_id: screen.id,
				view_screen_id: screen.id,
			},
		});
		expect(configSetResponse.ok()).toBeTruthy();
		const configSet = await configSetResponse.json();

		try {
			await page.goto(`/workspaces/${workspace.id}/backlog`);
			await page.click("#global-create-button");
			const title = page.locator("#work-item-title");
			await expect(title).toBeVisible({ timeout: 5000 });
			await title.fill(`Create-with-component ${suffix}`);

			// Non-required custom fields live behind the additional-fields toggle.
			const toggle = page.getByTestId("create-additional-fields-toggle");
			await expect(toggle).toBeVisible({ timeout: 10000 });
			await toggle.click();

			// Pick the asset in the combobox.
			const pickerInput = page.getByPlaceholder("Select asset", { exact: true });
			await expect(pickerInput).toBeVisible({ timeout: 10000 });
			await pickerInput.click();
			const dropdown = page.getByTestId("picker-dropdown");
			await expect(dropdown).toBeVisible();
			await dropdown.getByText(`Bearing ${suffix}`).click();

			// Capture the create request and submit.
			const createRequestPromise = page.waitForRequest(
				(req) => req.method() === "POST" && /\/api\/v2\/items$/.test(req.url()),
			);
			await page.locator("#create-modal-submit").click();
			const createRequest = await createRequestPromise;
			const requestBody = createRequest.postDataJSON();
			console.log(
				"CREATE PAYLOAD custom_field_values:",
				JSON.stringify(requestBody.custom_field_values),
			);
			expect(requestBody.custom_field_values).toBeDefined();
			expect(requestBody.custom_field_values[String(field.id)]).toMatchObject({
				id: asset.id,
			});

			// The create response must already carry the asset value.
			const createResponse = await createRequest.response();
			expect(createResponse?.ok()).toBeTruthy();
			const createdItem = (await createResponse?.json())?.data;
			expect(createdItem?.custom_field_values?.[String(field.id)]).toMatchObject({
				id: asset.id,
			});

			// And the value must persist on reload.
			const refreshed = await request.get(`/api/v2/items/${createdItem.id}`, {
				headers: SEC_FETCH,
			});
			expect(refreshed.ok()).toBeTruthy();
			const refreshedItem = (await refreshed.json()).data;
			expect(refreshedItem.custom_field_values[String(field.id)]).toMatchObject({
				id: asset.id,
				title: `Bearing ${suffix}`,
			});

			// The item detail screen must show the component.
			const itemPage = new ItemPage(page);
			await itemPage.gotoWorkspaceBacklog(String(workspace.id));
			await itemPage.openItemDetailModal(`Create-with-component ${suffix}`);
			const dialog = page.getByTestId("item-detail");
			await expect(dialog).toContainText(`Component ${suffix}`);
			await expect(
				dialog.getByTestId(`item-custom-field-display-${field.id}`),
			).toContainText(`Bearing ${suffix}`);
		} finally {
			await request
				.delete(`/api/configuration-sets/${configSet.id}`, { headers: SEC_FETCH })
				.catch(() => {});
			await request
				.delete(`/api/screens/${screen.id}`, { headers: SEC_FETCH })
				.catch(() => {});
			await request
				.delete(`/api/v2/custom-fields/${field.id}`, { headers: SEC_FETCH })
				.catch(() => {});
		}
	});

	test("value survives an item type switch after picking", async ({ page, request }) => {
		test.slow();
		const suffix = `${Date.now()}-type-switch`;

		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace(`asset-cf-switch-${suffix}`),
		);

		const setResponse = await request.post("/api/v2/asset-sets", {
			headers: SEC_FETCH,
			data: { name: `Asset set ${suffix}` },
		});
		expect(setResponse.ok()).toBeTruthy();
		const set = (await setResponse.json()).data;

		const typeResponse = await request.post(`/api/v2/asset-sets/${set.id}/types`, {
			headers: SEC_FETCH,
			data: { name: "Component", description: "type", icon: "Box", color: "#1f6feb" },
		});
		expect(typeResponse.ok()).toBeTruthy();
		const assetType = (await typeResponse.json()).data;

		const assetResponse = await request.post(`/api/v2/asset-sets/${set.id}/assets`, {
			headers: SEC_FETCH,
			data: { title: `Bearing ${suffix}`, asset_type_id: assetType.id },
		});
		expect(assetResponse.ok()).toBeTruthy();
		const asset = (await assetResponse.json()).data;

		const field = await createCustomFieldViaAPI(request, {
			name: `Component ${suffix}`,
			field_type: "asset",
			required: false,
			options: JSON.stringify({ asset_set_id: set.id }),
		});

		const screenResponse = await request.post("/api/screens", {
			headers: SEC_FETCH,
			data: { name: `Asset CF screen ${suffix}`, description: "e2e" },
		});
		expect(screenResponse.ok()).toBeTruthy();
		const screen = await screenResponse.json();
		const fieldsResponse = await request.put(`/api/screens/${screen.id}/fields`, {
			headers: SEC_FETCH,
			data: [
				{
					field_type: "custom",
					field_identifier: String(field.id),
					display_order: 0,
					is_required: false,
					field_width: "full",
				},
			],
		});
		expect(fieldsResponse.ok()).toBeTruthy();

		const configSetResponse = await request.post("/api/configuration-sets", {
			headers: SEC_FETCH,
			data: {
				name: `Asset CF config ${suffix}`,
				description: "e2e",
				workspace_ids: [workspace.id],
				create_screen_id: screen.id,
				edit_screen_id: screen.id,
				view_screen_id: screen.id,
			},
		});
		expect(configSetResponse.ok()).toBeTruthy();
		const configSet = await configSetResponse.json();

		try {
			await page.goto(`/workspaces/${workspace.id}/backlog`);
			await page.click("#global-create-button");
			const title = page.locator("#work-item-title");
			await expect(title).toBeVisible({ timeout: 5000 });
			await title.fill(`Switch type ${suffix}`);

			// Pick the component first, while the default type is selected.
			const toggle = page.getByTestId("create-additional-fields-toggle");
			await expect(toggle).toBeVisible({ timeout: 10000 });
			await toggle.click();
			const pickerInput = page.getByPlaceholder("Select asset", { exact: true });
			await expect(pickerInput).toBeVisible({ timeout: 10000 });
			await pickerInput.click();
			const dropdown = page.getByTestId("picker-dropdown");
			await expect(dropdown).toBeVisible();
			await dropdown.getByText(`Bearing ${suffix}`).click();

			// Then switch the item type — the field stays on the new type's screen.
			await page.getByTestId("create-item-type-chip").click();
			const options = page.getByTestId("create-item-type-chip-option");
			await options.first().waitFor({ state: "visible" });
			const count = await options.count();
			if (count < 2) throw new Error("need at least two item types for the switch");
			const currentlySelected = page.locator(
				'[data-testid="create-item-type-chip-option"][aria-selected="true"]',
			);
			const target = (await currentlySelected.count()) === 1 ? options.last() : options.first();
			await target.click();

			const createRequestPromise = page.waitForRequest(
				(req) => req.method() === "POST" && /\/api\/v2\/items$/.test(req.url()),
			);
			await page.locator("#create-modal-submit").click();
			const createRequest = await createRequestPromise;
			const requestBody = createRequest.postDataJSON();
			console.log(
				"TYPE-SWITCH PAYLOAD custom_field_values:",
				JSON.stringify(requestBody.custom_field_values),
			);
			expect(requestBody.custom_field_values[String(field.id)]).toMatchObject({
				id: asset.id,
			});
		} finally {
			await request
				.delete(`/api/configuration-sets/${configSet.id}`, { headers: SEC_FETCH })
				.catch(() => {});
			await request
				.delete(`/api/screens/${screen.id}`, { headers: SEC_FETCH })
				.catch(() => {});
			await request
				.delete(`/api/v2/custom-fields/${field.id}`, { headers: SEC_FETCH })
				.catch(() => {});
		}
	});
});
