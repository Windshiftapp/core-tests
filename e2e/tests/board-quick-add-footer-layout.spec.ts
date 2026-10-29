import { randomUUID } from "node:crypto";
import { createWorkspaceViaAPI } from "../fixtures/api-helpers";
import { expect, test } from "../fixtures/context-path";
import {
	assignWorkspaceRole,
	createWorkspaceUser,
	openSession,
} from "../fixtures/role-sessions";
import { generateWorkspace } from "../fixtures/test-data";

const SEC_FETCH = { "Sec-Fetch-Site": "same-origin" };

/**
 * The board quick-add footer packs two icon selectors plus cancel/create into a
 * column that is only ~300px wide. German labels ("Abbrechen"/"Erstellen") are
 * the longest seeded translations, so they are the layout's worst case.
 */
test("German quick-add footer keeps its actions inside the card", async ({
	browser,
	request,
}) => {
	const suffix = randomUUID().slice(0, 8);
	const workspace = await createWorkspaceViaAPI(
		request,
		generateWorkspace(`qa-footer-${suffix}`),
	);
	const user = await createWorkspaceUser(request, `qa-footer-${suffix}`);
	await assignWorkspaceRole(request, user.id, workspace.id, "Editor");
	const regionalResponse = await request.put(
		`/api/users/${user.id}/regional-settings`,
		{ headers: SEC_FETCH, data: { language: "de", timezone: "UTC" } },
	);
	expect(regionalResponse.ok()).toBeTruthy();

	const session = await openSession(browser, user);
	try {
		await session.page.goto(`/workspaces/${workspace.id}/board`);
		await expect(session.page.getByTestId("board-view")).toBeVisible();
		await session.page
			.getByTestId(/^board-column-add-/)
			.first()
			.click();

		const createButton = session.page.getByTestId("quick-add-create");
		await expect(createButton).toContainText("Erstellen");
		const footer = session.page.getByTestId("quick-add-footer");
		await expect(footer).toBeVisible();

		const { footerRight, createRight, footerScrollWidth, footerClientWidth } =
			await footer.evaluate((el) => {
				const create = el.querySelector<HTMLElement>(
					'[data-testid="quick-add-create"]',
				);
				return {
					footerRight: el.getBoundingClientRect().right,
					createRight: create?.getBoundingClientRect().right ?? Number.NaN,
					footerScrollWidth: el.scrollWidth,
					footerClientWidth: el.clientWidth,
				};
			});

		expect(
			createRight,
			"create button must not spill past the footer's right edge",
		).toBeLessThanOrEqual(footerRight + 0.5);
		expect(footerScrollWidth).toBeLessThanOrEqual(footerClientWidth);
	} finally {
		await session.context.close();
		const deletedWorkspace = await request.delete(
			`/api/v2/workspaces/${workspace.id}`,
			{ headers: SEC_FETCH },
		);
		expect(deletedWorkspace.status()).toBe(204);
		const deletedUser = await request.delete(`/api/users/${user.id}`, {
			headers: SEC_FETCH,
		});
		expect(deletedUser.ok()).toBeTruthy();
	}
});
