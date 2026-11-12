import { expect, test } from "../fixtures/context-path";

const SEC_FETCH = { "Sec-Fetch-Site": "same-origin" };

test("CSV import UI rejects a row whose mapped required field is blank", async ({
	request,
	page,
}) => {
	const stamp = Date.now();
	const assetTitle = `required-import-${stamp}`;
	const fieldName = `Required owner ${stamp}`;

	const setResponse = await request.post("/api/v2/asset-sets", {
		headers: SEC_FETCH,
		data: {
			name: `Required import ${stamp}`,
			description: "Browser CSV required-field regression",
		},
	});
	expect(
		setResponse.ok(),
		`create asset set: ${await setResponse.text()}`,
	).toBeTruthy();
	const assetSet = (await setResponse.json()).data;

	const typeResponse = await request.post(
		`/api/v2/asset-sets/${assetSet.id}/types`,
		{
			headers: SEC_FETCH,
			data: { name: `Server ${stamp}` },
		},
	);
	expect(
		typeResponse.ok(),
		`create asset type: ${await typeResponse.text()}`,
	).toBeTruthy();
	const assetType = (await typeResponse.json()).data;

	const fieldResponse = await request.post("/api/v2/custom-fields", {
		headers: SEC_FETCH,
		data: { name: fieldName, field_type: "text" },
	});
	expect(
		fieldResponse.ok(),
		`create custom field: ${await fieldResponse.text()}`,
	).toBeTruthy();
	const customField = (await fieldResponse.json()).data.custom_field;

	const attachResponse = await request.put(
		`/api/v2/asset-types/${assetType.id}/fields`,
		{
			headers: SEC_FETCH,
			data: {
				fields: [
					{
						custom_field_id: customField.id,
						is_required: true,
						display_order: 0,
					},
				],
			},
		},
	);
	expect(
		attachResponse.ok(),
		`attach required field: ${await attachResponse.text()}`,
	).toBeTruthy();

	await page.goto("/assets");
	await page.locator("#asset-set-select").click();
	await page.locator(`#asset-set-select-option-${assetSet.id}`).click();
	await page.getByTestId("asset-import-open").click();

	const typePicker = page.locator("#asset-import-type");
	await expect(typePicker).toBeVisible();
	await typePicker.click();
	const typeOption = page.locator(`#asset-import-type-option-${assetType.id}`);
	await expect(typeOption).toBeVisible({ timeout: 10_000 });
	await typeOption.click();
	await expect(typePicker).toContainText(assetType.name);

	await page.locator("#asset-import-file").setInputFiles({
		name: "required-field.csv",
		mimeType: "text/csv",
		buffer: Buffer.from(`Title,${fieldName}\n${assetTitle},\n`, "utf-8"),
	});

	const next = page.getByTestId("asset-import-next");
	await expect(next).toBeEnabled();
	await next.click();
	await page.getByTestId("asset-import-auto-map").click();
	await expect(next).toBeEnabled();
	await next.click();
	await expect(next).toBeEnabled();
	await next.click();

	await expect(page.locator("#asset-imported-count")).toHaveText("0", {
		timeout: 15_000,
	});
	await expect(page.locator("#asset-import-failed-count")).toHaveText("1");

	const listResponse = await request.get(
		`/api/v2/asset-sets/${assetSet.id}/assets?search=${encodeURIComponent(assetTitle)}`,
		{ headers: SEC_FETCH },
	);
	expect(listResponse.ok()).toBeTruthy();
	const list = await listResponse.json();
	expect(
		(list.data as Array<{ title: string }>).some(
			(asset) => asset.title === assetTitle,
		),
	).toBe(false);
});
