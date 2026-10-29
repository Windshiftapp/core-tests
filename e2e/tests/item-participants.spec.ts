import { createItemViaAPI, createWorkspaceViaAPI } from "../fixtures/api-helpers";
import { expect, test } from "../fixtures/context-path";
import { generateWorkspace } from "../fixtures/test-data";
import {
	attachRequestTypesToSection,
	createPortalChannel,
	createSimpleRequestType,
} from "../helpers/portal-setup";

const SEC_FETCH = { "Sec-Fetch-Site": "same-origin" };

// WI-1136: external request participants. An agent adds a participant by email,
// the chip persists across reload, and removal revokes it. Participants only
// make sense for requests that came from outside (portal or helpdesk email), so
// the section is hidden on internal items.
test.describe("Item request participants", () => {
	test("is hidden on internal items", async ({ page, request }) => {
		const suffix = `${Date.now()}-${Math.floor(Math.random() * 10000)}`;
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace(`participants-internal-${suffix}`),
		);
		const item = await createItemViaAPI(request, workspace.id, {
			title: `Internal item ${suffix}`,
		});

		await page.goto(`/workspaces/${workspace.id}/items/${item.id}`);

		await expect(page.getByTestId("status-field")).toBeVisible();
		await expect(page.getByTestId("item-participants")).toHaveCount(0);
		await expect(page.getByTestId("item-participant-add-toggle")).toHaveCount(0);
	});

	test("adds, persists, and removes an external participant", async ({
		page,
		request,
	}) => {
		const suffix = `${Date.now()}-${Math.floor(Math.random() * 10000)}`;
		const portal = await createPortalChannel(request, {
			slug: `participants-portal-${suffix}`,
			name: `Participants Portal ${suffix}`,
		});
		const requestType = await createSimpleRequestType(request, portal, {
			name: `Participant request ${suffix}`,
		});
		await attachRequestTypesToSection(request, portal, [requestType.id]);

		// Create the item through the production portal submission endpoint so it
		// carries a request type and the external-participants section renders.
		const submitResponse = await request.post(`/api/portal/${portal.slug}/submit`, {
			headers: SEC_FETCH,
			data: {
				request_type_id: requestType.id,
				title: `Participant item ${suffix}`,
				description: "Submitted through the portal.",
			},
		});
		expect(
			submitResponse.ok(),
			`submit: ${submitResponse.status()} ${await submitResponse.text()}`,
		).toBeTruthy();
		const itemId = (await submitResponse.json()).item_id as number;
		expect(itemId).toBeGreaterThan(0);
		const participantEmail = `participant-${suffix}@example.test`;

		await page.goto(`/workspaces/${portal.workspaceId}/items/${itemId}`);

		const section = page.getByTestId("item-participants");
		await expect(section).toBeVisible();
		await expect(page.getByTestId("item-participants-empty")).toBeVisible();

		// The add form stays hidden behind the subtle header toggle.
		await expect(page.getByTestId("item-participant-email")).toHaveCount(0);
		await page.getByTestId("item-participant-add-toggle").click();

		// Add by email.
		const addResponse = page.waitForResponse(
			(response) =>
				response.request().method() === "POST" &&
				new RegExp(`/api/v2/items/${itemId}/participants$`).test(
					response.url(),
				),
		);
		await page.getByTestId("item-participant-email").fill(participantEmail);
		await page.getByTestId("item-participant-add").click();

		const response = await addResponse;
		expect(
			response.ok(),
			`participant add: ${response.status()} ${await response.text()}`,
		).toBeTruthy();
		const participants = (await response.json()).data as Array<{
			portal_customer_id: number;
			customer_email: string;
		}>;
		const participant = participants.find(
			(entry) => entry.customer_email === participantEmail,
		);
		expect(
			participant,
			`participant list = ${JSON.stringify(participants)}`,
		).toBeTruthy();
		const customerId = participant!.portal_customer_id;

		await expect(page.getByTestId(`item-participant-${customerId}`)).toBeVisible();
		await expect(page.getByTestId(`item-participant-${customerId}`)).toContainText(
			participantEmail,
		);
		// The composer closes after a successful add.
		await expect(page.getByTestId("item-participant-email")).toHaveCount(0);

		// Reload proves persistence, not just optimistic rendering.
		await page.reload();
		await expect(page.getByTestId(`item-participant-${customerId}`)).toBeVisible();

		// Remove and confirm the chip is gone.
		const deleteResponse = page.waitForResponse(
			(response) =>
				response.request().method() === "DELETE" &&
				response.url().includes(`/participants/${customerId}`),
		);
		await page.getByTestId(`item-participant-remove-${customerId}`).click();
		await deleteResponse;
		await expect(page.getByTestId(`item-participant-${customerId}`)).toHaveCount(0);

		// The API agrees the participant is gone.
		const listResponse = await request.get(
			`/api/v2/items/${itemId}/participants`,
		);
		expect(listResponse.ok()).toBeTruthy();
		const remaining = (await listResponse.json()).data as Array<{
			portal_customer_id: number;
		}>;
		expect(
			remaining.some((entry) => entry.portal_customer_id === customerId),
		).toBeFalsy();
	});
});
