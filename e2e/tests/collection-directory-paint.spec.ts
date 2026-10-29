import { randomUUID } from "node:crypto";
import type { APIRequestContext } from "@playwright/test";
import {
	createCollectionViaAPI,
	createItemViaAPI,
	createWorkspaceViaAPI,
} from "../fixtures/api-helpers";
import { expect, logicalPath, test } from "../fixtures/context-path";
import { generateWorkspace } from "../fixtures/test-data";

const SEC_FETCH = { "Sec-Fetch-Site": "same-origin" };

/** The single cached directory page the shell, board and map all share. */
function isDirectoryRequest(url: URL): boolean {
	return (
		logicalPath(url.pathname) === "/api/v2/workspaces" &&
		url.searchParams.get("page_size") === "200"
	);
}

interface DirectoryScope {
	workspace: Awaited<ReturnType<typeof createWorkspaceViaAPI>>;
	collection: Awaited<ReturnType<typeof createCollectionViaAPI>>;
	parent: Awaited<ReturnType<typeof createItemViaAPI>>;
}

async function createDirectoryScope(
	request: APIRequestContext,
	suffix: string,
): Promise<DirectoryScope> {
	const workspace = await createWorkspaceViaAPI(
		request,
		generateWorkspace(suffix),
	);
	const parent = await createItemViaAPI(request, workspace.id, {
		title: `Directory paint ${suffix}`,
	});
	// A global collection over one workspace keeps quick-add dependent on the
	// directory: the referenced workspace is resolved from the cached page.
	const collection = await createCollectionViaAPI(request, {
		name: `Directory paint ${suffix}`,
		workspace_id: null,
		ql_query: `workspace = "${workspace.key}"`,
	});
	return { workspace, collection, parent };
}

async function cleanupScope(request: APIRequestContext, scope: DirectoryScope) {
	const collection = await request.delete(
		`/api/v2/collections/${scope.collection.id}`,
		{ headers: SEC_FETCH },
	);
	expect(collection.ok(), await collection.text()).toBeTruthy();
	const workspace = await request.delete(
		`/api/v2/workspaces/${scope.workspace.id}`,
		{ headers: SEC_FETCH },
	);
	expect(workspace.ok(), await workspace.text()).toBeTruthy();
}

async function holdDirectory(page: import("@playwright/test").Page) {
	let releaseDirectory!: () => void;
	let directoryRequested!: () => void;
	let markDone!: () => void;
	const started = new Promise<void>((resolve) => {
		directoryRequested = resolve;
	});
	const released = new Promise<void>((resolve) => {
		releaseDirectory = resolve;
	});
	const done = new Promise<void>((resolve) => {
		markDone = resolve;
	});
	await page.route(isDirectoryRequest, async (route) => {
		const response = await route.fetch();
		directoryRequested();
		await released;
		await route.fulfill({ response });
		markDone();
	});
	return { started, releaseDirectory, done };
}

test.describe("collection directory paint barrier", () => {
	test("board paints while the directory is pending and quick-add enables after release", async ({
		page,
		request,
	}) => {
		const suffix = `dir-board-${randomUUID().slice(0, 8)}`;
		const scope = await createDirectoryScope(request, suffix);
		const held = await holdDirectory(page);
		try {
			await page.goto(`/collections/${scope.collection.id}/board`);
			await held.started;

			// Board content paints without waiting for the directory response.
			await expect(page.getByTestId("board-view")).toBeVisible();
			await expect(page.getByTestId("board-column").first()).toBeVisible();

			// Quick-add opens, but its workspace selector cannot offer choices
			// until the directory resolves.
			await page
				.getByTestId(/^board-column-add-/)
				.first()
				.click();
			await expect(page.getByTestId("quick-add-workspace")).toBeDisabled();

			held.releaseDirectory();
			await held.done;
			await expect(page.getByTestId("quick-add-workspace")).toBeEnabled();
			await page.getByTestId("quick-add-workspace").click();
			await expect(
				page.getByTestId(`quick-add-workspace-option-${scope.workspace.id}`),
			).toBeVisible();
		} finally {
			await page.unroute(isDirectoryRequest);
			await cleanupScope(request, scope);
		}
	});

	test("map paints while the directory is pending", async ({ page, request }) => {
		const suffix = `dir-map-${randomUUID().slice(0, 8)}`;
		const scope = await createDirectoryScope(request, suffix);
		const held = await holdDirectory(page);
		try {
			await page.goto(`/collections/${scope.collection.id}/map`);
			await held.started;

			await expect(page.getByTestId("map-view")).toBeVisible();
			await expect(
				page.getByTestId(`map-backbone-item-${scope.parent.id}`),
			).toBeVisible();

			held.releaseDirectory();
			await held.done;
		} finally {
			await page.unroute(isDirectoryRequest);
			await cleanupScope(request, scope);
		}
	});

	test("board renders its handled state when the directory request fails", async ({
		page,
		request,
	}) => {
		const suffix = `dir-fail-${randomUUID().slice(0, 8)}`;
		const scope = await createDirectoryScope(request, suffix);
		try {
			await page.route(isDirectoryRequest, async (route) => {
				await route.fulfill({
					status: 500,
					contentType: "application/json",
					body: JSON.stringify({ error: "directory unavailable" }),
				});
			});

			await page.goto(`/collections/${scope.collection.id}/board`);

			await expect(page.getByTestId("board-view")).toBeVisible();
			await expect(page.getByTestId("board-column").first()).toBeVisible();
			// No creatable workspace survives the failed directory, so the
			// quick-add affordance stays absent instead of opening empty.
			await expect(page.getByTestId(/^board-column-add-/)).toHaveCount(0);
		} finally {
			await page.unroute(isDirectoryRequest);
			await cleanupScope(request, scope);
		}
	});
});
