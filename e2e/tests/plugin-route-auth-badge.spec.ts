import { randomUUID } from "node:crypto";
import { expect, test } from "../fixtures/errors";
import {
	deleteCapabilityPlugins,
	installRouteAuthPlugin,
} from "../fixtures/plugin-zip";

// WI-1820: a plugin route declares its credential in the manifest, and the
// module settings list shows the resolved auth per route.
test("module settings shows the declared auth badge per plugin route", async ({
	page,
	request,
	allowConsoleError,
}) => {
	// Pre-existing: the instance-id endpoint is only configured when license
	// enforcement is on, so a fresh e2e server answers 500.
	allowConsoleError(/\/api\/plugins\/instance-id/);
	allowConsoleError(/Failed to load instance ID/);
	const name = `route-auth-${randomUUID().slice(0, 8)}`;
	try {
		await installRouteAuthPlugin(request, name, [
			{ method: "GET", path: "/public", auth: "none" },
			{ method: "GET", path: "/private", auth: "session" },
			{ method: "GET", path: "/admin", auth: "admin" },
		]);

		await page.goto("/admin/modules");

		await expect(page.getByTestId(`plugin-route-auth-${name}-0`)).toHaveText(
			"Public",
		);
		await expect(page.getByTestId(`plugin-route-auth-${name}-1`)).toHaveText(
			"Session",
		);
		await expect(page.getByTestId(`plugin-route-auth-${name}-2`)).toHaveText(
			"Admin",
		);
	} finally {
		await deleteCapabilityPlugins(request, name);
	}
});
