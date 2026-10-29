import { createWorkspaceViaAPI } from "../fixtures/api-helpers";
import { expect, test } from "../fixtures/context-path";
import { generateWorkspace } from "../fixtures/test-data";

/**
 * The authenticated app footer moved out of the fixed shell into the end of
 * opted-in page content. These tests pin the observable contract: it is
 * reachable on document surfaces, keeps its links, and is absent on
 * viewport-filling surfaces like the board.
 */
test.describe("authenticated app footer", () => {
	test("the homepage footer is reachable by scrolling and links to About", async ({
		page,
	}) => {
		await page.goto("/");
		await expect(page.getByTestId("homepage")).toBeVisible({ timeout: 10000 });

		const footer = page.getByTestId("app-footer");
		await footer.scrollIntoViewIfNeeded();
		await expect(footer).toBeVisible();

		await page.getByTestId("footer-about").click();
		await expect(page).toHaveURL(/\/about$/);
		await expect(page.getByTestId("app-footer")).toBeVisible();
	});

	test("workspace settings ends with the footer", async ({ page, request }) => {
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace("footer-settings"),
		);
		await page.goto(`/workspaces/${workspace.id}/settings`);
		await expect(page.getByTestId("workspace-settings-module-general")).toBeVisible({
			timeout: 10000,
		});

		const footer = page.getByTestId("app-footer");
		await footer.scrollIntoViewIfNeeded();
		await expect(footer).toBeVisible();
	});

	test("the workspace overview background wraps its content above the footer", async ({
		page,
		request,
	}) => {
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace("footer-overview"),
		);
		await page.setViewportSize({ width: 1280, height: 480 });
		await page.goto(`/workspaces/${workspace.id}/overview`);
		const wrapper = page.getByTestId("workspace-welcome");
		await expect(wrapper).toBeVisible({ timeout: 10000 });

		const footer = page.getByTestId("app-footer");
		await footer.scrollIntoViewIfNeeded();
		await expect(footer).toBeVisible();

		// The gradient/background wrapper must grow with its content. A flex
		// column on the scroll container used to shrink it to the viewport,
		// clipping the background and painting the overflowing page content
		// over the footer.
		const layout = await wrapper.evaluate((element) => {
			const content = element.querySelector(".workspace-welcome");
			const wrapperBox = element.getBoundingClientRect();
			return {
				wrapperHeight: wrapperBox.height,
				contentBottom: content
					? content.getBoundingClientRect().bottom - wrapperBox.top
					: 0,
			};
		});
		expect(layout.contentBottom).toBeLessThanOrEqual(layout.wrapperHeight + 1);

		const wrapperBox = await wrapper.boundingBox();
		const footerBox = await footer.boundingBox();
		if (!wrapperBox || !footerBox) throw new Error("missing layout boxes");
		expect(wrapperBox.y + wrapperBox.height).toBeLessThanOrEqual(
			footerBox.y + 1,
		);
	});

	test("the board has no footer", async ({ page, request }) => {
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace("footer-board"),
		);
		await page.goto(`/workspaces/${workspace.id}/board`);
		await expect(page.getByTestId("board-view")).toBeVisible({ timeout: 10000 });

		await expect(page.getByTestId("app-footer")).toHaveCount(0);
	});
});
