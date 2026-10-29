import { createItemViaAPI, createWorkspaceViaAPI } from "../fixtures/api-helpers";
import type { APIRequestContext } from "../fixtures/context-path";
import { expect, test } from "../fixtures/context-path";
import {
	attachRequestTypesToSection,
	createPortalChannel,
	createSimpleRequestType,
} from "../helpers/portal-setup";

const SEC_FETCH = { "Sec-Fetch-Site": "same-origin" };
const BASE_URL = process.env.BASE_URL || "http://localhost:8080";

async function createCannedResponseViaAPI(
	request: APIRequestContext,
	workspaceId: number,
	data: { name: string; body: string; is_private?: boolean },
) {
	const response = await request.post(
		`${BASE_URL}/api/v2/workspaces/${workspaceId}/canned-responses`,
		{
			headers: SEC_FETCH,
			data: { is_private: false, ...data },
		},
	);
	expect(
		response.status(),
		`create canned response failed: ${await response.text()}`,
	).toBe(201);
	return (await response.json()).data as { id: number; name: string };
}

/**
 * Build a portal channel plus request type and submit a request through the
 * production portal endpoint, so the item carries a request type and counts as
 * an external customer request.
 */
async function submitPortalRequest(request: APIRequestContext, suffix: string) {
	const portal = await createPortalChannel(request, {
		slug: `canned-${suffix}`,
		name: `Canned Portal ${suffix}`,
	});
	const requestType = await createSimpleRequestType(request, portal, {
		name: `Canned request ${suffix}`,
	});
	await attachRequestTypesToSection(request, portal, [requestType.id]);

	const submitResponse = await request.post(
		`/api/portal/${portal.slug}/submit`,
		{
			headers: SEC_FETCH,
			data: {
				request_type_id: requestType.id,
				title: `Canned item ${suffix}`,
				description: "Submitted through the portal.",
			},
		},
	);
	expect(
		submitResponse.ok(),
		`submit: ${submitResponse.status()} ${await submitResponse.text()}`,
	).toBeTruthy();
	const itemId = (await submitResponse.json()).item_id as number;
	expect(itemId).toBeGreaterThan(0);
	return { portal, itemId };
}

/**
 * WI-1138: canned responses are support-reply snippets, so the composer only
 * offers them on external customer requests and only when the workspace has
 * snippets to insert.
 */
test.describe("canned responses journey", () => {
	test("manager creates a snippet and an agent inserts it into a customer request", async ({
		page,
		request,
	}) => {
		const stamp = Date.now();
		const { portal, itemId } = await submitPortalRequest(request, `${stamp}`);

		// Manager creates the canned response in workspace settings.
		await page.goto(`/workspaces/${portal.workspaceId}/settings/canned-responses`);
		await expect(page.getByTestId("canned-response-add")).toBeVisible({
			timeout: 15_000,
		});
		await page.getByTestId("canned-response-add").click();
		await page.getByTestId("canned-response-name").fill(`greeting-${stamp}`);
		await page
			.getByTestId("canned-response-body")
			.fill("Hello from the canned journey");
		await page.getByTestId("canned-response-save").click();

		const row = page.getByTestId("canned-response-row");
		await expect(row).toContainText(`greeting-${stamp}`);
		await expect(row).toContainText("Public");

		// Agent opens the customer request and inserts the snippet.
		await page.goto(`/workspaces/${portal.workspaceId}/items/${itemId}`);
		const section = page.getByTestId("comments-section");
		await expect(section).toBeVisible({ timeout: 15_000 });

		await page.getByTestId("canned-response-picker").click();
		const option = page
			.getByTestId("canned-response-picker-menu")
			.getByText(`greeting-${stamp}`);
		await expect(option).toBeVisible({ timeout: 15_000 });
		await option.click();

		await expect(page.getByTestId("comment-composer")).toContainText(
			"Hello from the canned journey",
		);
		await expect(page.getByTestId("comment-submit")).toBeEnabled();
		await page.getByTestId("comment-submit").click();

		await expect(section.getByTestId("comment-item")).toHaveCount(1);
		await expect(section.getByTestId("comment-item")).toContainText(
			"Hello from the canned journey",
		);
	});

	test("hides the picker on internal items even when snippets exist", async ({
		page,
		request,
	}) => {
		const suffix = `${Date.now()}-${Math.floor(Math.random() * 10000)}`;
		const workspace = await createWorkspaceViaAPI(request, {
			name: `canned-internal-${suffix}`,
			key: `CI${suffix.replace(/\D/g, "").slice(-6)}`.toUpperCase(),
			description: "WI-1138 internal item",
		});
		await createCannedResponseViaAPI(request, workspace.id, {
			name: `internal-snippet-${suffix}`,
			body: "Internal snippet body",
		});
		const item = await createItemViaAPI(request, workspace.id, {
			title: `Internal item ${suffix}`,
		});

		await page.goto(`/workspaces/${workspace.id}/items/${item.id}`);
		await expect(page.getByTestId("comments-section")).toBeVisible({
			timeout: 15_000,
		});

		await expect(page.getByTestId("canned-response-picker")).toHaveCount(0);
	});

	test("hides the picker on customer requests when the workspace has no snippets", async ({
		page,
		request,
	}) => {
		const suffix = `${Date.now()}-${Math.floor(Math.random() * 10000)}`;
		const { portal, itemId } = await submitPortalRequest(request, suffix);

		const responsesLoaded = page.waitForResponse(
			(response) =>
				response.request().method() === "GET" &&
				response
					.url()
					.includes(`/workspaces/${portal.workspaceId}/canned-responses`),
		);

		await page.goto(`/workspaces/${portal.workspaceId}/items/${itemId}`);
		await expect(page.getByTestId("comments-section")).toBeVisible({
			timeout: 15_000,
		});

		const loaded = await responsesLoaded;
		expect(loaded.ok()).toBeTruthy();
		const body = await loaded.json();
		expect(body.data ?? []).toEqual([]);

		await expect(page.getByTestId("canned-response-picker")).toHaveCount(0);
	});
});
