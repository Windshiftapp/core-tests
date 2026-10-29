import { createWorkspaceViaAPI } from "../fixtures/api-helpers";
import { expect, test } from "../fixtures/context-path";
import { generateWorkspace } from "../fixtures/test-data";

test.describe("Workspace look and feel", () => {
	test("persists the selected workspace gradient across reload", async ({
		page,
		request,
	}) => {
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace("look-and-feel"),
		);

		await page.goto(`/workspaces/${workspace.id}/look-and-feel`);
		const view = page.getByTestId("workspace-look-and-feel");
		const none = page.getByTestId("gradient-swatch-0");
		const gradient = page.getByTestId("gradient-swatch-3");

		await expect(view).toBeVisible();
		await expect(none).toHaveAttribute("aria-pressed", "true");
		await expect(gradient).toHaveAttribute("aria-pressed", "false");

		const saveResponse = page.waitForResponse(
			(response) =>
				response.request().method() === "PUT" &&
				response
					.url()
					.endsWith(`/api/workspaces/${workspace.id}/homepage/layout`) &&
				response.ok(),
		);
		await gradient.click();
		await saveResponse;

		await expect(gradient).toHaveAttribute("aria-pressed", "true");
		await expect(none).toHaveAttribute("aria-pressed", "false");
		await expect(view).toHaveCSS("background-image", /linear-gradient/);

		await page.reload();
		await expect(view).toBeVisible();
		await expect(gradient).toHaveAttribute("aria-pressed", "true");
		await expect(none).toHaveAttribute("aria-pressed", "false");
		await expect(view).toHaveCSS("background-image", /linear-gradient/);
	});
});
