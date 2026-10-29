import {
	createCustomFieldViaAPI,
	createUserViaAPI,
	createWorkspaceViaAPI,
} from "../fixtures/api-helpers";
import { expect, test } from "../fixtures/context-path";
import { shot } from "../helpers/screenshot";

const SEC_FETCH = { "Sec-Fetch-Site": "same-origin" };
const FIELD_COUNT = 18;

interface FieldSpec {
	name: string;
	field_type: string;
	options?: string;
	value: unknown;
}

/**
 * A wide asset type is a layout stress test: the detail sidebar and the edit
 * modal both have to scroll instead of pushing the shell or hiding fields.
 * This spec attaches 18 mixed-type custom fields to one asset and checks that
 * the last field and the save button stay reachable.
 */
test("asset detail and edit form scroll with many custom fields", async ({
	request,
	page,
}, testInfo) => {
	test.setTimeout(120_000);
	const stamp = `${Date.now()}-${testInfo.workerIndex}-${testInfo.repeatEachIndex}`;
	const assetTitle = `Wide application ${stamp}`;

	const workspace = await createWorkspaceViaAPI(request, {
		name: `Wide asset journey ${stamp}`,
		key: `WAJ${stamp.replace(/\D/g, "").slice(-6)}`,
		description: "many custom fields e2e",
	});
	await createUserViaAPI(request, {
		email: `wide-owner-${stamp}@example.test`,
		username: `wide-owner-${stamp}`,
		first_name: "Wide",
		last_name: `Owner${stamp}`,
		password_hash: "TestPass123!",
	});

	const setResponse = await request.post("/api/v2/asset-sets", {
		headers: SEC_FETCH,
		data: { name: `Wide applications ${stamp}` },
	});
	expect(setResponse.status(), await setResponse.text()).toBe(201);
	const set = (await setResponse.json()).data as { id: number };

	const typeResponse = await request.post(`/api/v2/asset-sets/${set.id}/types`, {
		headers: SEC_FETCH,
		data: { name: `Wide application ${stamp}` },
	});
	expect(typeResponse.status(), await typeResponse.text()).toBe(201);
	const assetType = (await typeResponse.json()).data as { id: number };

	// Five field shapes cycle across 18 fields so the type exercises the
	// renderer's text, number, date, boolean, and select paths.
	const specs: FieldSpec[] = [];
	for (let i = 0; i < FIELD_COUNT; i++) {
		const n = String(i + 1).padStart(2, "0");
		switch (i % 5) {
			case 0:
				specs.push({
					name: `Text ${n} ${stamp}`,
					field_type: "text",
					value: `Value ${n}`,
				});
				break;
			case 1:
				specs.push({
					name: `Number ${n} ${stamp}`,
					field_type: "number",
					value: i + 1,
				});
				break;
			case 2:
				specs.push({
					name: `Date ${n} ${stamp}`,
					field_type: "date",
					value: "2024-06-15",
				});
				break;
			case 3:
				specs.push({
					name: `Flag ${n} ${stamp}`,
					field_type: "boolean",
					value: i % 2 === 0,
				});
				break;
			default:
				specs.push({
					name: `Choice ${n} ${stamp}`,
					field_type: "select",
					options: JSON.stringify({
						next_id: 3,
						items: [
							{ id: 1, label: "Alpha" },
							{ id: 2, label: "Beta" },
						],
					}),
					value: 1,
				});
				break;
		}
	}

	const values: Record<string, unknown> = {};
	const attachedFields: Array<{ custom_field_id: number; is_required: boolean; display_order: number }> =
		[];
	for (const [index, spec] of specs.entries()) {
		const field = await createCustomFieldViaAPI(request, {
			name: spec.name,
			field_type: spec.field_type,
			options: spec.options,
		});
		values[String(field.id)] = spec.value;
		attachedFields.push({
			custom_field_id: field.id,
			is_required: false,
			display_order: index,
		});
	}
	const lastFieldID = attachedFields[attachedFields.length - 1].custom_field_id;

	const attachResponse = await request.put(
		`/api/v2/asset-types/${assetType.id}/fields`,
		{ headers: SEC_FETCH, data: { fields: attachedFields } },
	);
	expect(attachResponse.status(), await attachResponse.text()).toBe(200);

	const assetResponse = await request.post(
		`/api/v2/asset-sets/${set.id}/assets`,
		{
			headers: SEC_FETCH,
			data: {
				asset_type_id: assetType.id,
				title: assetTitle,
				custom_field_values: values,
			},
		},
	);
	expect(assetResponse.status(), await assetResponse.text()).toBe(201);
	const asset = (await assetResponse.json()).data as { id: number };

	// ── Detail sidebar scrolls internally ────────────────────────────────
	await page.goto("/assets");
	await page.locator("#asset-set-select").click();
	await page.locator(`#asset-set-select-option-${set.id}`).click();
	await page.getByTestId(`asset-title-${asset.id}`).click();

	const detailPane = page.getByTestId("asset-detail-pane");
	const detailScroll = page.getByTestId("asset-detail-scroll");
	await expect(detailScroll).toBeVisible();
	// Wait for the last field to mount; the type's field definitions load
	// asynchronously and an early measurement sees a non-overflowing pane.
	await expect(page.getByTestId(`asset-detail-field-${lastFieldID}`)).toBeAttached();

	const scrollMetrics = await detailScroll.evaluate((el) => ({
		scrollHeight: el.scrollHeight,
		clientHeight: el.clientHeight,
	}));
	expect(
		scrollMetrics.scrollHeight,
		"detail pane must overflow and scroll with 18 fields",
	).toBeGreaterThan(scrollMetrics.clientHeight);

	// The pane itself stays inside the viewport; only its body scrolls.
	const paneBox = await detailPane.boundingBox();
	const viewport = page.viewportSize();
	expect(paneBox, "detail pane has a layout box").not.toBeNull();
	expect(paneBox!.height).toBeLessThanOrEqual(viewport!.height + 1);
	await expect(page.getByTestId(`asset-detail-field-${lastFieldID}`)).not.toBeInViewport();
	await shot(page, "01-detail-many-fields-top", { locator: detailPane });

	await detailScroll.evaluate((el) => {
		el.scrollTop = el.scrollHeight;
	});
	await expect(page.getByTestId(`asset-detail-field-${lastFieldID}`)).toBeInViewport();
	// The header is outside the scroll container, so it stays put.
	await expect(detailPane).toContainText(assetTitle);
	await shot(page, "02-detail-many-fields-bottom", { locator: detailPane });

	// ── Edit modal scrolls to its save button ────────────────────────────
	await detailPane.getByTestId("asset-edit").click();
	const modal = page.getByTestId("asset-form-modal");
	await expect(modal).toBeVisible();
	await expect(page.getByTestId(`custom-field-input-${lastFieldID}`)).toBeAttached();
	// Opening the form must not auto-open any custom-field picker; the asset
	// form omitted the autoOpenPickers={false} guard the other forms pass.
	await expect(page.getByTestId("picker-dropdown")).toHaveCount(0);

	const modalMetrics = await modal.evaluate((el) => ({
		scrollHeight: el.scrollHeight,
		clientHeight: el.clientHeight,
	}));
	expect(
		modalMetrics.scrollHeight,
		"edit modal must overflow and scroll with 18 fields",
	).toBeGreaterThan(modalMetrics.clientHeight);
	await shot(page, "03-edit-many-fields-top");

	await modal.evaluate((el) => {
		el.scrollTop = el.scrollHeight;
	});
	const submit = page.getByTestId("asset-submit");
	await submit.scrollIntoViewIfNeeded();
	await expect(submit).toBeInViewport();
	await shot(page, "04-edit-many-fields-bottom");
});
