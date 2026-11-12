import { randomUUID } from "node:crypto";
import { expect, test } from "../fixtures/errors";
import {
	deleteCapabilityPlugins,
	installPermissionPlugin,
} from "../fixtures/plugin-zip";

// WI-1821: a plugin declares the host functions it may call, and the module
// settings list shows each requested permission and whether it is in effect.
test("module settings shows the declared host-function permissions", async ({
	page,
	request,
	allowConsoleError,
}) => {
	// Pre-existing: the instance-id endpoint is only configured when license
	// enforcement is on, so a fresh e2e server answers 500.
	allowConsoleError(/\/api\/plugins\/instance-id/);
	allowConsoleError(/Failed to load instance ID/);
	const name = `permissions-${randomUUID().slice(0, 8)}`;
	try {
		await installPermissionPlugin(request, name, [
			"process.exec",
			"network.egress",
		]);

		await page.goto("/admin/modules");

		// normalizePermissions sorts the catalog, so network.egress precedes
		// process.exec. Both are granted because license enforcement is off.
		const network = page.getByTestId(`plugin-permission-${name}-0`);
		await expect(network).toHaveAttribute("data-permission", "network.egress");
		await expect(network).toHaveAttribute("data-granted", "true");
		await expect(network).toHaveText("network.egress");

		const process = page.getByTestId(`plugin-permission-${name}-1`);
		await expect(process).toHaveAttribute("data-permission", "process.exec");
		await expect(process).toHaveAttribute("data-granted", "true");
		await expect(process).toHaveText("process.exec");
	} finally {
		await deleteCapabilityPlugins(request, name);
	}
});
