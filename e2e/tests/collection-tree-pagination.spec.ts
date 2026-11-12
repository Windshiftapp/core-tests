import type { APIRequestContext } from "@playwright/test";
import {
	createCollectionViaAPI,
	createItemViaAPI,
	createWorkspaceViaAPI,
	listItemTypesViaAPI,
} from "../fixtures/api-helpers";
import { expect, test } from "../fixtures/errors";

// GH #400: a flat, mixed-level page split parent/child pairs across the
// pagination boundary, so a parent whose child landed on a later page rendered
// as a flat leaf. The tree now pages one item-type level and loads descendants
// by ancestry, so the base page can only truncate additional roots.
async function hierarchyItemTypes(request: APIRequestContext) {
	const itemTypes = await listItemTypesViaAPI(request);
	const byKey = (key: string) =>
		itemTypes.find((type: { builtin_key?: string }) => type.builtin_key === key);
	return { epic: byKey("epic"), story: byKey("story") };
}

test("pages the tree's base level without losing the hierarchy", async ({
	page,
	request,
}) => {
	const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
	const workspace = await createWorkspaceViaAPI(request, {
		name: `Tree pagination ${stamp}`,
		key: `TPG${stamp.toString().slice(-5)}`.toUpperCase(),
		description: "Playwright tree base pagination coverage",
	});
	const types = await hierarchyItemTypes(request);

	// Parent first so it stays on the first page.
	const parent = await createItemViaAPI(request, workspace.id, {
		title: `Paged parent ${stamp}`,
		item_type_id: types.epic.id,
	});
	const child = await createItemViaAPI(request, workspace.id, {
		title: `Paged child ${stamp}`,
		parent_id: parent.id,
		item_type_id: types.story.id,
	});

	// One more epic than the tree's 100-item base page, so the last one spills
	// onto page 2.
	const fillers = [];
	for (let index = 0; index < 100; index += 1) {
		fillers.push(
			await createItemViaAPI(request, workspace.id, {
				title: `Paged epic ${index} ${stamp}`,
				item_type_id: types.epic.id,
			}),
		);
	}
	const spilled = fillers[fillers.length - 1];

	const collection = await createCollectionViaAPI(request, {
		name: `Paged collection ${stamp}`,
		workspace_id: workspace.id,
		ql_query: `title ~ "${stamp}"`,
	});

	await page.goto(
		`/workspaces/${workspace.id}/collections/${collection.id}/tree`,
	);

	// The child is not on the base page; it is fetched by ancestry, so the
	// parent still renders an expander and the child is visible without any
	// continuation click.
	await expect(page.getByTestId(`tree-row-${parent.id}`)).toBeVisible();
	await expect(page.getByTestId(`tree-expand-${parent.id}`)).toBeVisible();
	await expect(page.getByTestId(`tree-row-${child.id}`)).toBeVisible();

	// The extra root needs the base-page continuation.
	await expect(page.getByTestId(`tree-row-${spilled.id}`)).toHaveCount(0);
	const loadMore = page.getByTestId("tree-load-more-button");
	await expect(loadMore).toBeVisible();
	await loadMore.click();
	await expect(page.getByTestId(`tree-row-${spilled.id}`)).toBeVisible();
	await expect(page.getByTestId("tree-load-more-button")).toHaveCount(0);
});
