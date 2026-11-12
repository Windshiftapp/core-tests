import type { APIRequestContext } from "@playwright/test";
import {
	createCollectionViaAPI,
	createItemViaAPI,
	createWorkspaceViaAPI,
	listItemTypesViaAPI,
} from "../fixtures/api-helpers";
import { expect, test } from "../fixtures/errors";

// The tree is rooted at one item-type level, so a fixture hierarchy must use
// adjacent item types (Epic -> Story -> Task); untyped items all default to
// level 0 and would render as siblings.
async function hierarchyItemTypes(request: APIRequestContext) {
	const itemTypes = await listItemTypesViaAPI(request);
	const byKey = (key: string) =>
		itemTypes.find((type: { builtin_key?: string }) => type.builtin_key === key);
	return { epic: byKey("epic"), story: byKey("story"), task: byKey("task") };
}

test("navigates and drills into the same hierarchy across tree, map, and roadmap", async ({
	page,
	request,
}) => {
	const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
	const workspace = await createWorkspaceViaAPI(request, {
		name: `Hierarchy views ${stamp}`,
		key: `HV${stamp.toString().slice(-6)}`.toUpperCase(),
		description: "Playwright collection hierarchy coverage",
	});
	const types = await hierarchyItemTypes(request);
	const parent = await createItemViaAPI(request, workspace.id, {
		title: `Parent initiative ${stamp}`,
		item_type_id: types.epic.id,
	});
	const child = await createItemViaAPI(request, workspace.id, {
		title: `Child delivery ${stamp}`,
		parent_id: parent.id,
		item_type_id: types.story.id,
	});
	const collection = await createCollectionViaAPI(request, {
		name: `Hierarchy collection ${stamp}`,
		workspace_id: workspace.id,
		ql_query: `title ~ "${stamp}"`,
	});
	const configuration = await request.put(
		`/api/v2/collections/${collection.id}/board-configuration`,
		{
			headers: { "Sec-Fetch-Site": "same-origin" },
			data: {
				columns: [],
				backlog_status_ids: [],
				list_columns: [],
				card_fields: [],
				show_rightmost_column_last_50: false,
			},
		},
	);
	expect(configuration.ok()).toBeTruthy();

	const collectionBase = `/workspaces/${workspace.id}/collections/${collection.id}`;

	await page.goto(`${collectionBase}/tree`);
	await expect(page.getByTestId("tree-view")).toBeVisible();
	// The key cell keeps a real href but opens the global item modal on a plain
	// left click, so assert the modal instead of a route change.
	await page.getByTestId(`list-cell-key-${parent.id}`).click();
	const modal = page.getByTestId("item-detail-modal");
	await expect(modal).toBeVisible();
	await expect(modal.getByTestId("item-title-edit")).toHaveText(parent.title);

	await page.goto(`${collectionBase}/map`);
	await expect(page.getByTestId("map-view")).toBeVisible();
	await expect(page.getByTestId(`map-backbone-item-${parent.id}`)).toHaveText(
		parent.title,
	);
	await page.getByTestId(`map-drill-down-${parent.id}`).click();
	await expect(page).toHaveURL(new RegExp(`[?&]parent=${parent.id}(?:&|$)`));
	await expect(page.getByTestId(`map-backbone-item-${child.id}`)).toHaveText(
		child.title,
	);

	await page.goto(`${collectionBase}/roadmap`);
	await expect(page.getByTestId("roadmap-view")).toBeVisible();
	await page.getByTestId(`roadmap-item-${child.id}`).click();
	await expect(modal).toBeVisible();
	await expect(modal.getByTestId("item-title-edit")).toHaveText(child.title);
});

test("roadmap expand-all reveals a collapsed subtree", async ({
	page,
	request,
}) => {
	const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
	const workspace = await createWorkspaceViaAPI(request, {
		name: `Roadmap expand ${stamp}`,
		key: `RE${stamp.toString().slice(-6)}`.toUpperCase(),
		description: "Playwright roadmap expand-all coverage",
	});
	const types = await hierarchyItemTypes(request);
	const epic = await createItemViaAPI(request, workspace.id, {
		title: `Expand epic ${stamp}`,
		item_type_id: types.epic.id,
	});
	const story = await createItemViaAPI(request, workspace.id, {
		title: `Expand story ${stamp}`,
		parent_id: epic.id,
		item_type_id: types.story.id,
	});
	const task = await createItemViaAPI(request, workspace.id, {
		title: `Expand task ${stamp}`,
		parent_id: story.id,
		item_type_id: types.task.id,
	});

	await page.goto(`/workspaces/${workspace.id}/roadmap`);
	await expect(page.getByTestId("roadmap-view")).toBeVisible();
	await expect(page.getByTestId(`roadmap-item-${story.id}`)).toBeVisible();
	// The task starts collapsed under the story; roots auto-expand, so the first
	// click collapses everything and the second expands the whole tree.
	await expect(page.getByTestId(`roadmap-item-${task.id}`)).toHaveCount(0);

	await page.getByTestId("roadmap-expand-toggle").click();
	await expect(page.getByTestId(`roadmap-item-${story.id}`)).toHaveCount(0);

	await page.getByTestId("roadmap-expand-toggle").click();
	await expect(page.getByTestId(`roadmap-item-${task.id}`)).toBeVisible();
});

test("indents nested tree rows by hierarchy depth", async ({
	page,
	request,
}) => {
	const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
	const workspace = await createWorkspaceViaAPI(request, {
		name: `Tree indent ${stamp}`,
		key: `TI${stamp.toString().slice(-6)}`.toUpperCase(),
		description: "Playwright tree indentation coverage",
	});
	const types = await hierarchyItemTypes(request);
	const parent = await createItemViaAPI(request, workspace.id, {
		title: `Indent parent ${stamp}`,
		item_type_id: types.epic.id,
	});
	const child = await createItemViaAPI(request, workspace.id, {
		title: `Indent child ${stamp}`,
		parent_id: parent.id,
		item_type_id: types.story.id,
	});
	const grandchild = await createItemViaAPI(request, workspace.id, {
		title: `Indent grandchild ${stamp}`,
		parent_id: child.id,
		item_type_id: types.task.id,
	});
	const collection = await createCollectionViaAPI(request, {
		name: `Indent collection ${stamp}`,
		workspace_id: workspace.id,
		ql_query: `title ~ "${stamp}"`,
	});

	await page.goto(
		`/workspaces/${workspace.id}/collections/${collection.id}/tree`,
	);
	await expect(page.getByTestId(`tree-row-${parent.id}`)).toBeVisible();
	// Roots with children start expanded, so the child row is already there.
	await expect(page.getByTestId(`tree-row-${child.id}`)).toBeVisible();
	await page.getByTestId(`tree-expand-${child.id}`).click();
	await expect(page.getByTestId(`tree-row-${grandchild.id}`)).toBeVisible();

	const titleX = (id: number) =>
		page
			.getByTestId(`workspace-item-title-${id}`)
			.evaluate((element) => element.getBoundingClientRect().x);
	const parentX = await titleX(parent.id);
	const childX = await titleX(child.id);
	const grandchildX = await titleX(grandchild.id);

	// Each depth steps the title right by the same indent instead of leaving
	// every title at the expander column's x position.
	const firstStep = childX - parentX;
	const secondStep = grandchildX - childX;
	expect(firstStep).toBeGreaterThan(0);
	expect(secondStep).toBeCloseTo(firstStep, 1);
});

test("tree expansion survives a reload", async ({ page, request }) => {
	const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
	const workspace = await createWorkspaceViaAPI(request, {
		name: `Tree persistence ${stamp}`,
		key: `TP${stamp.toString().slice(-6)}`.toUpperCase(),
		description: "Playwright tree expansion persistence coverage",
	});
	const types = await hierarchyItemTypes(request);
	const epic = await createItemViaAPI(request, workspace.id, {
		title: `Persist epic ${stamp}`,
		item_type_id: types.epic.id,
	});
	const story = await createItemViaAPI(request, workspace.id, {
		title: `Persist story ${stamp}`,
		parent_id: epic.id,
		item_type_id: types.story.id,
	});
	const task = await createItemViaAPI(request, workspace.id, {
		title: `Persist task ${stamp}`,
		parent_id: story.id,
		item_type_id: types.task.id,
	});
	const collection = await createCollectionViaAPI(request, {
		name: `Persist collection ${stamp}`,
		workspace_id: workspace.id,
		ql_query: `title ~ "${stamp}"`,
	});

	const url = `/workspaces/${workspace.id}/collections/${collection.id}/tree`;
	await page.goto(url);
	await expect(page.getByTestId(`tree-row-${epic.id}`)).toBeVisible();
	// Roots start expanded, so the story is visible and the task is collapsed.
	await expect(page.getByTestId(`tree-row-${story.id}`)).toBeVisible();
	await expect(page.getByTestId(`tree-row-${task.id}`)).toHaveCount(0);

	// Wait for the debounced view-state PATCH that carries the expansion, not
	// the level save that may still be in flight from mount.
	const saved = page.waitForResponse((response) => {
		if (
			!response.url().includes("/api/v2/users/me/preferences") ||
			response.request().method() !== "PATCH"
		) {
			return false;
		}
		return (response.request().postData() ?? "").includes("expanded_ids");
	});
	await page.getByTestId(`tree-expand-${story.id}`).click();
	await expect(page.getByTestId(`tree-row-${task.id}`)).toBeVisible();
	await saved;

	await page.reload();
	await expect(page.getByTestId(`tree-row-${story.id}`)).toBeVisible();
	await expect(page.getByTestId(`tree-row-${task.id}`)).toBeVisible();
});

test("dragging at the map edge scrolls the hierarchy horizontally", {
	tag: "@critical-browser",
}, async ({ page, request }) => {
	await page.setViewportSize({ width: 800, height: 720 });

	const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
	const workspace = await createWorkspaceViaAPI(request, {
		name: `Map drag scroll ${stamp}`,
		key: `MS${stamp.toString().slice(-6)}`.toUpperCase(),
		description: "Playwright map drag-scroll coverage",
	});
	const types = await hierarchyItemTypes(request);
	const parents = [];
	for (let index = 0; index < 7; index += 1) {
		parents.push(
			await createItemViaAPI(request, workspace.id, {
				title: `Map parent ${index} ${stamp}`,
				item_type_id: types.epic.id,
			}),
		);
	}
	const child = await createItemViaAPI(request, workspace.id, {
		title: `Map draggable child ${stamp}`,
		parent_id: parents[0].id,
		item_type_id: types.story.id,
	});

	await page.goto(`/workspaces/${workspace.id}/map`);
	await expect(page.getByTestId("map-view")).toBeVisible();

	const scrollContainer = page.getByTestId("map-scroll-container");
	const initialScroll = await scrollContainer.evaluate((element) => ({
		clientWidth: element.clientWidth,
		scrollLeft: element.scrollLeft,
		scrollWidth: element.scrollWidth,
	}));
	expect(
		initialScroll.scrollWidth,
		`map overflow metrics: ${JSON.stringify(initialScroll)}`,
	).toBeGreaterThan(initialScroll.clientWidth);

	const draggable = page.getByTestId(`draggable-item-${child.id}`);
	await expect(draggable).toHaveAttribute("draggable", "true");
	const draggableBox = await draggable.boundingBox();
	const containerBox = await scrollContainer.boundingBox();
	if (!draggableBox || !containerBox) {
		throw new Error("Map drag source and scroll container must be visible");
	}

	const startX = draggableBox.x + draggableBox.width / 2;
	const startY = draggableBox.y + draggableBox.height / 2;
	const edgeX = Math.min(800, containerBox.x + containerBox.width) - 8;
	await page.mouse.move(startX, startY);
	await page.mouse.down();
	await page.mouse.move(startX + 12, startY + 12);
	await page.mouse.move(edgeX, startY + 12, { steps: 12 });

	await expect
		.poll(() => scrollContainer.evaluate((element) => element.scrollLeft))
		.toBeGreaterThan(initialScroll.scrollLeft);

	await page.keyboard.press("Escape");
	await page.mouse.up();
});
