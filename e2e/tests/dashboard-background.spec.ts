import { expect, test } from "../fixtures/context-path";

test.describe("Personal dashboard background", () => {
	test("persists the selected dashboard gradient across reload", async ({
		page,
	}) => {
		await page.addInitScript(() => {
			localStorage.setItem("windshift-dashboard-onboarding-dismissed", "true");
		});
		await page.goto("/");
		const homepage = page.getByTestId("homepage");
		await expect(homepage).toHaveAttribute("data-ready", "true", {
			timeout: 15_000,
		});

		await page.getByTestId("dashboard-customize-toggle").click();
		await page.getByTestId("customization-category-background").click();

		const none = page.getByTestId("dashboard-gradient-swatch-0");
		const gradient = page.getByTestId("dashboard-gradient-swatch-3");
		await expect(none).toHaveAttribute("aria-pressed", "true");
		await expect(gradient).toHaveAttribute("aria-pressed", "false");

		const saveResponse = page.waitForResponse(
			(response) =>
				response.request().method() === "PUT" &&
				response.url().endsWith("/api/user/dashboard-layout") &&
				response.ok(),
		);
		await gradient.click();
		await saveResponse;

		await expect(gradient).toHaveAttribute("aria-pressed", "true");
		await expect(none).toHaveAttribute("aria-pressed", "false");
		await expect(homepage).toHaveCSS("background-image", /linear-gradient/);

		await page.reload();
		await expect(homepage).toHaveAttribute("data-ready", "true", {
			timeout: 15_000,
		});
		await expect(homepage).toHaveCSS("background-image", /linear-gradient/);
	});
});
