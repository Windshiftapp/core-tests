import {
	createItemViaAPI,
	createWorkspaceViaAPI,
} from "../fixtures/api-helpers";
import { expect, test } from "../fixtures/context-path";

/**
 * The composer placeholder is a `::before` pseudo-element gated on the
 * document being empty. The selector originally matched any paragraph whose
 * only element child was a `<br>`; because `:only-child` ignores text nodes,
 * a paragraph with text plus a hard break still matched and the placeholder
 * painted over the typed content.
 */
test.describe("comment composer placeholder", () => {
	test("placeholder disappears after typing a hard break", async ({
		page,
		request,
	}) => {
		const stamp = Date.now();
		const workspace = await createWorkspaceViaAPI(request, {
			name: `composer-placeholder-${stamp}`,
			key: `CP${stamp.toString().slice(-6)}`.toUpperCase(),
			description: "composer placeholder regression",
		});
		const item = await createItemViaAPI(request, workspace.id, {
			title: `Placeholder ${stamp}`,
		});

		await page.goto(`/workspaces/${workspace.id}/items/${item.id}`);
		await expect(page.getByTestId("comments-section")).toBeVisible({
			timeout: 15_000,
		});

		const composer = page.getByTestId("comment-composer");
		await expect(composer).toHaveAttribute("data-ready", "true", {
			timeout: 15_000,
		});

		const placeholderContent = () =>
			composer.evaluate((el) => {
				const proseMirror = el.querySelector(".ProseMirror");
				return proseMirror
					? getComputedStyle(proseMirror, "::before").content
					: null;
			});

		// The empty composer shows the placeholder, so the assertion below can
		// distinguish "hidden" from "never rendered".
		await expect.poll(placeholderContent).toBe('"Add a comment..."');

		await composer.click();
		await page.keyboard.type("approved the ranking of storymapping.app");
		await page.keyboard.down("Shift");
		await page.keyboard.press("Enter");
		await page.keyboard.up("Shift");
		await page.keyboard.type("docz is");

		await expect.poll(placeholderContent).toBe("none");
		await expect(composer).toContainText("docz is");
	});
});
