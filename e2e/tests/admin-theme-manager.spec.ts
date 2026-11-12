import { restoreActiveTheme } from "../fixtures/api-helpers";
import { expect, type APIRequestContext, test } from "../fixtures/context-path";
import { acquireGlobalLock } from "../fixtures/global-lock";

/**
 * Admin theme manager (/admin/themes). Creates a theme with the default
 * palette, activates it, and deletes it. The previously active theme is
 * restored so the shared environment keeps its appearance.
 */

const SEC_FETCH = { "Sec-Fetch-Site": "same-origin" };

interface Theme {
	id: number;
	name: string;
	is_default: boolean;
	is_active: boolean;
}

async function listThemes(request: APIRequestContext): Promise<Theme[]> {
	const response = await request.get("/api/themes", { headers: SEC_FETCH });
	expect(response.ok()).toBeTruthy();
	return await response.json();
}

test.describe("Admin theme manager", () => {
	test("creates, activates, and deletes a theme", async ({ page, request }) => {
		// The active theme is a global singleton, so serialize against the other
		// theme spec instead of deactivating each other mid-assertion.
		const release = await acquireGlobalLock("active-theme");
		const suffix = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
		const name = `E2E Theme ${suffix}`;
		let originalActive: Theme | undefined;
		let createdId = 0;

		try {
			originalActive = (await listThemes(request)).find((theme) => theme.is_active);
			expect(originalActive, "an active theme exists").toBeDefined();

			await page.goto("/admin/themes");
			await page.getByTestId("theme-add").click();

			const dialog = page.locator('[role="dialog"]');
			await expect(dialog).toBeVisible();
			await dialog.locator("#name").fill(name);

			// Wait for the create response so the id is read from the request that
			// actually persisted the theme instead of racing a list refetch.
			const createResponse = page.waitForResponse(
				(response) =>
					response.request().method() === "POST" &&
					new URL(response.url()).pathname.endsWith("/api/themes"),
			);
			await page.getByTestId("dialog-confirm").click();
			const response = await createResponse;
			expect(response.status(), "created theme status").toBe(201);
			createdId = (await response.json()).id;

			const card = page.getByTestId(`theme-card-${createdId}`);
			await expect(card).toBeVisible();

			// Activate and verify server-side state plus the visible card.
			await page.getByTestId(`theme-activate-${createdId}`).click();
			await expect
				.poll(async () => (await listThemes(request)).find((t) => t.id === createdId)?.is_active)
				.toBe(true);
			await page.reload();
			await expect(page.getByTestId(`theme-card-${createdId}`)).toBeVisible();

			// Delete the non-default theme after confirming.
			await page.getByTestId(`theme-delete-${createdId}`).click();
			await expect(page.getByTestId("dialog-confirm")).toBeVisible();
			await page.getByTestId("dialog-confirm").click();
			await expect(page.getByTestId(`theme-card-${createdId}`)).toHaveCount(0);
			await expect
				.poll(async () =>
					listThemes(request).then((themes) => !themes.some((t) => t.id === createdId)),
				)
				.toBe(true);
			createdId = 0;
		} finally {
			// Fall back to a name lookup so a theme that persisted without a captured
			// id is still cleaned up.
			const leftover =
				createdId ||
				(await listThemes(request)
					.then((themes) => themes.find((theme) => theme.name === name)?.id)
					.catch(() => undefined)) ||
				0;
			if (leftover) {
				await request.delete(`/api/themes/${leftover}`, { headers: SEC_FETCH });
			}
			await restoreActiveTheme(request, originalActive);
			await release();
		}
	});
});
