import { randomUUID } from "node:crypto";
import {
	createWorkspaceViaAPI,
	listItemTypesViaAPI,
	listWorkspaceItemTypesViaAPI,
} from "../fixtures/api-helpers";
import { type APIRequestContext, expect, test } from "../fixtures/context-path";
import { generateWorkspace } from "../fixtures/test-data";

const SEC_FETCH = { "Sec-Fetch-Site": "same-origin" };

type ItemType = {
	id: number;
	hierarchy_level: number;
};

async function createParent(
	request: APIRequestContext,
	workspaceId: number,
	itemTypeId: number,
	title: string,
) {
	const response = await request.post("/api/v2/items", {
		headers: SEC_FETCH,
		data: {
			workspace_id: workspaceId,
			item_type_id: itemTypeId,
			title,
		},
	});
	expect(
		response.ok(),
		`create map parent: ${response.status()} ${await response.text()}`,
	).toBeTruthy();
	return (await response.json()).data as { id: number };
}

test("map quick-add creates and persists a child item", async ({
	page,
	request,
}) => {
	const suffix = `map-create-${randomUUID().slice(0, 8)}`;
	const workspace = await createWorkspaceViaAPI(
		request,
		generateWorkspace(suffix),
	);
	// Use the workspace's bound catalog: global types are not necessarily
	// available in a freshly created workspace.
	const itemTypes = (await listWorkspaceItemTypesViaAPI(
		request,
		workspace.id,
	)) as ItemType[];
	const parentType = itemTypes.find(
		(candidate) =>
			candidate.hierarchy_level >= 0 &&
			itemTypes.some(
				(child) =>
					child.hierarchy_level === -1 ||
					child.hierarchy_level === candidate.hierarchy_level + 1,
			),
	);
	if (!parentType) {
		throw new Error("No item type accepts children");
	}

	const parent = await createParent(
		request,
		workspace.id,
		parentType.id,
		`${suffix} parent`,
	);
	const childTitle = `${suffix} child`;

	await page.goto(`/workspaces/${workspace.id}/map`);
	await expect(page.getByTestId("map-view")).toBeVisible();
	await expect(
		page.getByTestId(`map-backbone-item-${parent.id}`),
	).toBeVisible();
	await page.getByTestId(`map-add-card-${parent.id}`).click();
	await page.getByTestId(`quick-add-title-${parent.id}`).fill(childTitle);

	const createResponsePromise = page.waitForResponse(
		(response) =>
			response.url().endsWith("/api/v2/items") &&
			response.request().method() === "POST",
	);
	await page.getByTestId("quick-add-create").click();
	const createResponse = await createResponsePromise;
	expect(
		createResponse.ok(),
		`map quick-add: ${createResponse.status()} ${await createResponse.text()}`,
	).toBeTruthy();
	const child = (await createResponse.json()).data as { id: number };

	await expect(page.getByTestId(`draggable-item-${child.id}`)).toContainText(
		childTitle,
	);
	await page.reload();
	await expect(page.getByTestId(`draggable-item-${child.id}`)).toContainText(
		childTitle,
	);
});

// The default workspace configuration set intentionally excludes the level-0
// Initiative type. A workspace can still hold Initiative items (imports, API),
// and the level picker then offers level 0 from the filtered counts. The add
// card must work there too: the parent type is not in the workspace-bound
// catalog, so its level has to come from the selected hierarchy level.
test("map add card works when the parent type is not workspace-bound", async ({
	page,
	request,
}) => {
	const suffix = `map-unbound-${randomUUID().slice(0, 8)}`;
	const workspace = await createWorkspaceViaAPI(
		request,
		generateWorkspace(suffix),
	);
	const globalTypes = (await listItemTypesViaAPI(request)) as ItemType[];
	const levelZeroType = globalTypes.find(
		(candidate) => candidate.hierarchy_level === 0,
	);
	if (!levelZeroType) throw new Error("No level-0 item type in the catalog");
	// The type must not be workspace-bound for this to reproduce the bug.
	const workspaceTypes = (await listWorkspaceItemTypesViaAPI(
		request,
		workspace.id,
	)) as ItemType[];
	expect(workspaceTypes.some((type) => type.id === levelZeroType.id)).toBe(
		false,
	);

	const parent = await createParent(
		request,
		workspace.id,
		levelZeroType.id,
		`${suffix} initiative`,
	);

	await page.goto(`/workspaces/${workspace.id}/map`);
	await expect(page.getByTestId("map-view")).toBeVisible();
	// Level 0 is the shallowest level with matches, so it is the default view.
	await expect(
		page.getByTestId(`map-backbone-item-${parent.id}`),
	).toBeVisible();
	await page.getByTestId(`map-add-card-${parent.id}`).click();
	await expect(
		page.getByTestId(`quick-add-title-${parent.id}`),
	).toBeVisible();
});
