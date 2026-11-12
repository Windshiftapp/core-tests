import { randomUUID } from "node:crypto";
import {
	createCollectionViaAPI,
	createWorkspaceViaAPI,
} from "../fixtures/api-helpers";
import { expect, test } from "../fixtures/context-path";
import { generateWorkspace } from "../fixtures/test-data";

/**
 * A card created while a collection filter hides it must still surface its
 * item key and offer to open the item, so the user can fix the item (for
 * example by adding the missing label) without leaving the collection.
 */
test("hidden quick-add card toast shows the item key and opens the item modal", async ({
	page,
	request,
}) => {
	const suffix = randomUUID().slice(0, 8);
	const workspace = await createWorkspaceViaAPI(
		request,
		generateWorkspace(`hidden-${suffix}`),
	);
	// A filter that matches nothing makes the freshly created card a non-member
	// of the collection while the board itself stays usable.
	const collection = await createCollectionViaAPI(request, {
		name: `Hidden card ${suffix}`,
		workspace_id: workspace.id,
		ql_query: `title ~ "never-matches-${suffix}"`,
	});

	await page.goto(`/collections/${collection.id}/board`);
	await expect(page.getByTestId("board-view")).toBeVisible();

	await page.getByTestId(/^board-column-add-/).first().click();
	const title = `Hidden card ${suffix}`;
	await page.getByTestId(/^quick-add-title-/).fill(title);

	const createResponsePromise = page.waitForResponse(
		(response) =>
			response.url().endsWith("/api/v2/items") &&
			response.request().method() === "POST",
	);
	await page.getByTestId("quick-add-create").click();
	const createResponse = await createResponsePromise;
	expect(createResponse.ok()).toBeTruthy();
	const item = (await createResponse.json()).data as {
		id: number;
		workspace_key: string;
		workspace_item_number: number;
	};
	const itemKey = `${item.workspace_key}-${item.workspace_item_number}`;

	const hiddenToast = page.getByTestId("toast").first();
	await expect(hiddenToast).toHaveAttribute("data-toast-variant", "info");
	await expect(hiddenToast).toContainText(itemKey);
	await expect(page.getByTestId("toast-message-info")).toContainText(
		"hidden by the collection filters",
	);

	await hiddenToast.click();
	await expect(page.getByTestId("item-detail-ready")).toBeVisible();
	await expect(page.getByTestId("item-detail")).toContainText(itemKey);
});
