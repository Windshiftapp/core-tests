import {
	createCollectionViaAPI,
	createItemViaAPI,
	createWorkspaceViaAPI,
} from "../fixtures/api-helpers";
import { expect, test } from "../fixtures/errors";

test("item detail sets the document title and restores it on close", async ({
	page,
	request,
}) => {
	const stamp = Date.now();
	const workspace = await createWorkspaceViaAPI(request, {
		name: `Title item ${stamp}`,
		key: `DT${stamp.toString().slice(-6)}`.toUpperCase(),
		description: "Playwright document title coverage",
	});
	const item = await createItemViaAPI(request, workspace.id, {
		title: `Titled item ${stamp}`,
	});
	const collection = await createCollectionViaAPI(request, {
		name: `Title collection ${stamp}`,
		workspace_id: workspace.id,
		ql_query: `title ~ "${stamp}"`,
	});

	await page.goto(
		`/workspaces/${workspace.id}/collections/${collection.id}/tree`,
	);
	await expect(page.getByTestId("tree-view")).toBeVisible();
	const siteTitle = await page.title();

	await page.getByTestId(`list-cell-key-${item.id}`).click();
	const modal = page.getByTestId("item-detail-modal");
	await expect(modal).toBeVisible();
	await expect(page).toHaveTitle(
		`${workspace.key}-${item.workspace_item_number} ${item.title} - ${siteTitle}`,
	);

	await page.keyboard.press("Escape");
	await expect(modal).toBeHidden();
	await expect(page).toHaveTitle(siteTitle);
});
