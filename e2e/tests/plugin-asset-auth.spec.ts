import { randomUUID } from "node:crypto";
import { authenticateAdminRequest } from "../fixtures/api-helpers";
import { expect, test } from "../fixtures/context-path";
import { deleteCapabilityPlugins, installAssetPlugin } from "../fixtures/plugin-zip";

const BASE_URL = process.env.BASE_URL || "http://localhost:8080";
const PLUGIN_PREFIX = "asset-auth";

function html(testId: string, label: string): string {
	return `<!doctype html><html><body><div data-testid="${testId}">${label}</div></body></html>`;
}

// A plugin asset's auth comes from its manifest policy, not from the route
// table. The entry document and its subresources must share an auth bucket.
test("plugin assets enforce their declared auth policy", async ({ page, request, browser }) => {
	test.setTimeout(90_000);

	await authenticateAdminRequest(request);

	const suffix = `${Date.now()}${randomUUID().slice(0, 4)}`;
	const pluginName = `${PLUGIN_PREFIX}-${suffix}`;

	await deleteCapabilityPlugins(request, PLUGIN_PREFIX);
	try {
		await installAssetPlugin(
			request,
			pluginName,
			[
				{ path: "public/*", auth: "none" },
				{ path: "admin/*", auth: "admin" },
			],
			{
				"public/open.html": html("public-asset", "public asset"),
				"admin/secret.html": html("admin-asset", "admin asset"),
				"internal/secret.html": html("internal-asset", "internal asset"),
			},
		);

		// An authenticated admin can load the admin-only asset.
		const adminResponse = await page.goto(
			`${BASE_URL}/api/plugins/${pluginName}/assets/admin/secret.html`,
		);
		expect(adminResponse?.status()).toBe(200);
		await expect(page.getByTestId("admin-asset")).toBeVisible();

		// A fresh, anonymous context can load the public asset...
		const anonymous = await browser.newContext({
			baseURL: BASE_URL,
			storageState: { cookies: [], origins: [] },
		});
		try {
			const anonPage = await anonymous.newPage();

			const publicResponse = await anonPage.goto(
				`${BASE_URL}/api/plugins/${pluginName}/assets/public/open.html`,
			);
			expect(publicResponse?.status()).toBe(200);
			await expect(anonPage.getByTestId("public-asset")).toBeVisible();

			// ...but not the admin asset, and not an undeclared asset, which
			// defaults to session auth.
			const adminDenied = await anonPage.goto(
				`${BASE_URL}/api/plugins/${pluginName}/assets/admin/secret.html`,
			);
			expect(adminDenied?.status()).toBe(401);

			const internalDenied = await anonPage.goto(
				`${BASE_URL}/api/plugins/${pluginName}/assets/internal/secret.html`,
			);
			expect(internalDenied?.status()).toBe(401);
		} finally {
			await anonymous.close();
		}
	} finally {
		await deleteCapabilityPlugins(request, pluginName);
	}
});
