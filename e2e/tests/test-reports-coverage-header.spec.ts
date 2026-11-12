import { createWorkspaceViaAPI } from "../fixtures/api-helpers";
import { expect, test } from "../fixtures/context-path";
import { generateWorkspace } from "../fixtures/test-data";

/**
 * The coverage tab header places two labelled Selects and a label-less
 * Configure button in a single row. The button's invisible label must match the
 * selects' label so all controls share a vertical center. When the spacer did
 * not match the FormField label, the button rendered lower than the selects.
 */
test.describe("Test reports coverage header alignment", () => {
	test("Configure button is vertically centered with the collection and filter selects", async ({
		page,
		request,
	}) => {
		const workspace = await createWorkspaceViaAPI(request, generateWorkspace());

		await page.goto(`/workspaces/${workspace.id}/tests/reports?subtab=coverage`);

		const configure = page.getByTestId("coverage-configure");
		await expect(configure).toBeVisible({ timeout: 15_000 });

		const buttonBox = await configure.boundingBox();
		expect(buttonBox).not.toBeNull();
		const buttonCenter = buttonBox!.y + buttonBox!.height / 2;

		for (const selectId of [
			"coverage-collection-select",
			"coverage-filter-select",
		]) {
			const select = page.locator(`#${selectId}`);
			await expect(select).toBeVisible();

			const selectBox = await select.boundingBox();
			expect(selectBox).not.toBeNull();
			const selectCenter = selectBox!.y + selectBox!.height / 2;

			expect(
				Math.abs(buttonCenter - selectCenter),
				`Configure button center (${buttonCenter}) should align with #${selectId} center (${selectCenter})`,
			).toBeLessThanOrEqual(2);
		}
	});
});
