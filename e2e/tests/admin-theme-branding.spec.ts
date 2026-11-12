import { restoreActiveTheme } from "../fixtures/api-helpers";
import { expect, type APIRequestContext, test } from "../fixtures/context-path";
import { acquireGlobalLock } from "../fixtures/global-lock";

/**
 * Verifies the theme brand lockup (full logo vs mark + site title) and the
 * instance site title surfaced by the General settings page. Both the active
 * theme and the site title are global singletons, so the test holds the shared
 * active-theme lock while it mutates them and restores the originals after.
 */

const SEC_FETCH = { "Sec-Fetch-Site": "same-origin" };

interface Theme {
	id: number;
	name: string;
	is_active: boolean;
}

interface Branding {
	site_title: string;
}

async function listThemes(request: APIRequestContext): Promise<Theme[]> {
	const response = await request.get("/api/themes", { headers: SEC_FETCH });
	expect(response.ok()).toBeTruthy();
	return await response.json();
}

test.describe("Theme brand lockup and site title", () => {
	test("full logo replaces the title and mark_text shows the site title", async ({
		page,
		request,
	}) => {
		const release = await acquireGlobalLock("active-theme");
		const suffix = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
		const name = `E2E Brand Theme ${suffix}`;
		const siteTitle = `E2E Site ${suffix}`;
		let originalActive: Theme | undefined;
		let originalSiteTitle = "";
		let created: Theme | undefined;
		try {
			const brandingResponse = await request.get("/api/branding", { headers: SEC_FETCH });
			expect(brandingResponse.ok()).toBeTruthy();
			originalSiteTitle = ((await brandingResponse.json()) as Branding).site_title;

			originalActive = (await listThemes(request)).find((theme) => theme.is_active);
			expect(originalActive, "an active theme exists").toBeDefined();

			const baseTheme = {
				description: "Brand lockup fixture",
				nav_background_color_light: "#ffffff",
				nav_text_color_light: "#374151",
				nav_background_color_dark: "#1f2937",
				nav_text_color_dark: "#f3f4f6",
				logo_url: "/api/portal-assets/mark-fixture",
				logo_full_url: "/api/portal-assets/full-fixture",
			};

			const createResponse = await request.post("/api/themes", {
				headers: SEC_FETCH,
				data: { ...baseTheme, name, brand_layout: "full_logo" },
			});
			expect(createResponse.ok()).toBeTruthy();
			created = (await createResponse.json()) as Theme;

			const activate = await request.post(`/api/themes/${created.id}/activate`, {
				headers: SEC_FETCH,
			});
			expect(activate.ok()).toBeTruthy();

			const setTitle = await request.put("/api/admin/general-settings", {
				headers: SEC_FETCH,
				data: { site_title: siteTitle },
			});
			expect(setTitle.ok()).toBeTruthy();

			// Keep the sidebar expanded so the brand title renders.
			await page.addInitScript(() => {
				try {
					window.localStorage.setItem("windshift-nav-expanded", "true");
				} catch {
					// Storage is unavailable on non-origin documents.
				}
			});

			await page.goto("/");
			const navLogo = page.getByTestId("nav-logo");
			await expect(navLogo).toHaveAttribute("src", "/api/portal-assets/full-fixture");
			await expect(page.getByTestId("nav-brand-title")).toHaveCount(0);

			// Switching to mark_text shows the mark plus the instance site title.
			const switchLayout = await request.put(`/api/themes/${created.id}`, {
				headers: SEC_FETCH,
				data: { ...baseTheme, name, brand_layout: "mark_text", is_active: true },
			});
			expect(switchLayout.ok()).toBeTruthy();

			await page.reload();
			await expect(navLogo).toHaveAttribute("src", "/api/portal-assets/mark-fixture");
			await expect(page.getByTestId("nav-brand-title")).toHaveText(siteTitle);

			// The General settings page saves the same title and updates the
			// sidebar live through the admin-mutation refresh.
			await page.goto("/admin/general");
			const updatedTitle = `${siteTitle} Updated`;
			await page.getByTestId("general-site-title").fill(updatedTitle);
			await page.getByTestId("general-settings-save").click();
			await expect(page.getByTestId("nav-brand-title")).toHaveText(updatedTitle);
			await expect(page).toHaveTitle(updatedTitle);
		} finally {
			if (created) {
				await request.delete(`/api/themes/${created.id}`, { headers: SEC_FETCH });
			}
			await restoreActiveTheme(request, originalActive);
			await request.put("/api/admin/general-settings", {
				headers: SEC_FETCH,
				data: { site_title: originalSiteTitle },
			});
			await release();
		}
	});
});
