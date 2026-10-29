import { restoreActiveTheme } from "../fixtures/api-helpers";
import { expect, type APIRequestContext, test } from "../fixtures/context-path";

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
		const stamp = Date.now();
		const name = `E2E Theme ${stamp}`;
		const originalActive = (await listThemes(request)).find((theme) => theme.is_active);
		expect(originalActive, "an active theme exists").toBeDefined();
		let createdId = 0;

		try {
			await page.goto("/admin/themes");
			await page.getByTestId("theme-add").click();

			const dialog = page.locator('[role="dialog"]');
			await expect(dialog).toBeVisible();
			await dialog.locator("#name").fill(name);
			await page.getByTestId("dialog-confirm").click();

			const created = (await listThemes(request)).find((theme) => theme.name === name);
			expect(created, "created theme persisted").toBeDefined();
			createdId = created!.id;

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
			if (createdId) {
				await request.delete(`/api/themes/${createdId}`, { headers: SEC_FETCH });
			}
			await restoreActiveTheme(request, originalActive);
		}
	});
});
