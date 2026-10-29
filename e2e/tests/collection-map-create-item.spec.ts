import { randomUUID } from "node:crypto";
import {
	createWorkspaceViaAPI,
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
