import type { Page } from "@playwright/test";

/**
 * Resolves when the debounced per-user view-state PATCH completes.
 *
 * View preferences are written through a debounced store, so a reload that
 * follows a settings change immediately can outrun the write. Start this
 * before the action that changes the setting, then await it before navigating
 * or reloading.
 */
export function preferenceSave(page: Page) {
	return page.waitForResponse(
		(response) =>
			response.url().includes("/api/v2/users/me/preferences") &&
			response.request().method() === "PATCH",
	);
}
