import { restoreActiveTheme } from "../fixtures/api-helpers";
import { expect, type APIRequestContext, test } from "../fixtures/context-path";

/**
 * Verifies that the active theme's light/dark logos are selected from the
 * resolved color mode, with a fallback to the light logo when no dark logo is
 * configured. Themes are created through the admin API so the app builds state
 * the same way an operator would. The active theme is global, so the scenarios
 * run in one serial test rather than racing across parallel workers.
 */

const SEC_FETCH = { "Sec-Fetch-Site": "same-origin" };

interface Theme {
	id: number;
	name: string;
	is_active: boolean;
	logo_url: string;
	logo_url_dark: string;
}

async function listThemes(request: APIRequestContext): Promise<Theme[]> {
	const response = await request.get("/api/themes", { headers: SEC_FETCH });
	expect(response.ok()).toBeTruthy();
	return await response.json();
}

test.describe("Theme logo selection", () => {
	test("uses the dark logo in dark mode and falls back to the light logo", async ({
		page,
		request,
	}) => {
		const stamp = Date.now();
		const name = `E2E Logo Theme ${stamp}`;
		const originalActive = (await listThemes(request)).find((theme) => theme.is_active);
		expect(originalActive, "an active theme exists").toBeDefined();

		let created: Theme | undefined;
		try {
			const createResponse = await request.post("/api/themes", {
				headers: SEC_FETCH,
				data: {
					name,
					description: "Logo selection fixture",
					nav_background_color_light: "#ffffff",
					nav_text_color_light: "#374151",
					nav_background_color_dark: "#1f2937",
					nav_text_color_dark: "#f3f4f6",
					logo_url: "/api/portal-assets/light-fixture",
					logo_url_dark: "/api/portal-assets/dark-fixture",
				},
			});
			expect(createResponse.ok()).toBeTruthy();
			created = (await createResponse.json()) as Theme;

			const activate = await request.post(`/api/themes/${created.id}/activate`, {
				headers: SEC_FETCH,
			});
			expect(activate.ok()).toBeTruthy();

			// Pin the user to the system color mode so emulateMedia drives the
			// resolved theme deterministically.
			await page.addInitScript(() => {
				try {
					window.localStorage.setItem("windshift-color-mode", "system");
				} catch {
					// Storage is unavailable on non-origin documents.
				}
			});

			await page.emulateMedia({ colorScheme: "dark" });
			await page.goto("/");
			const navLogo = page.getByTestId("nav-logo");
			await expect(navLogo).toHaveAttribute("src", "/api/portal-assets/dark-fixture");

			// Switching the resolved mode selects the light logo immediately.
			await page.emulateMedia({ colorScheme: "light" });
			await expect(navLogo).toHaveAttribute("src", "/api/portal-assets/light-fixture");

			// Clearing the dark logo must fall back to the light logo in dark mode.
			const clear = await request.put(`/api/themes/${created.id}`, {
				headers: SEC_FETCH,
				data: {
					name,
					description: "Logo selection fixture",
					nav_background_color_light: "#ffffff",
					nav_text_color_light: "#374151",
					nav_background_color_dark: "#1f2937",
					nav_text_color_dark: "#f3f4f6",
					logo_url: "/api/portal-assets/light-fixture",
					logo_url_dark: "",
					is_active: true,
				},
			});
			expect(clear.ok()).toBeTruthy();

			await page.emulateMedia({ colorScheme: "dark" });
			await page.reload();
			await expect(navLogo).toHaveAttribute("src", "/api/portal-assets/light-fixture");
		} finally {
			if (created) {
				await request.delete(`/api/themes/${created.id}`, { headers: SEC_FETCH });
			}
			await restoreActiveTheme(request, originalActive);
		}
	});
});
