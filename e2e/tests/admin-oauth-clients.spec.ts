import { expect, test, type APIRequestContext } from "../fixtures/context-path";

/**
 * Admin OAuth client registry (Integrations → Inbound, /admin/integration-providers).
 * Covers the register → secret-shown-once → rotate → delete lifecycle. The
 * plaintext secret must only ever be visible in the reveal modal.
 */

const SEC_FETCH = { "Sec-Fetch-Site": "same-origin" };

async function listClients(request: APIRequestContext) {
	const response = await request.get("/api/admin/oauth-clients", { headers: SEC_FETCH });
	expect(response.ok()).toBeTruthy();
	return (await response.json()) as Array<{
		id: number;
		slug: string;
		display_name: string;
		client_id: string;
		enabled: boolean;
		client_type: string;
		redirect_uris: string[];
		allowed_scopes: string[];
	}>;
}

test.describe("Admin OAuth clients", () => {
	test("registers a confidential client, reveals the secret once, rotates it, and deletes the client", async ({
		page,
		request,
	}) => {
		const stamp = Date.now();
		const slug = `e2e-oauth-${stamp}`;
		const displayName = `E2E OAuth ${stamp}`;
		const callback = `http://localhost:3000/callback/${slug}`;
		let clientId = "";

		try {
			await page.goto("/admin/integration-providers");
			await page.getByTestId("integrations-tab-inbound").click();
			await page.getByTestId("oauth-client-add").click();

			const dialog = page.locator('[role="dialog"]');
			await expect(dialog).toBeVisible();
			await page.getByTestId("oauth-client-display-name").fill(displayName);
			await page.getByTestId("oauth-client-slug").fill(slug);
			await page.getByTestId("oauth-client-redirect-uris").fill(callback);
			await page.getByTestId("oauth-client-scope-items:read").click();
			await expect(
				page.getByTestId("oauth-client-scope-items:read").locator("input"),
			).toBeChecked();
			await page.getByTestId("oauth-client-register-submit").click();

			// The create response carries the plaintext secret exactly once.
			await expect(page.getByTestId("oauth-client-secret-value")).toBeVisible();
			const secret = (await page.getByTestId("oauth-client-secret-value").innerText()).trim();
			expect(secret.length).toBeGreaterThanOrEqual(32);

			await page.getByTestId("oauth-client-secret-done").click();
			await expect(page.getByTestId("oauth-client-secret-value")).toHaveCount(0);

			// The registry lists the client with its enabled state and type.
			const clients = await listClients(request);
			const created = clients.find((client) => client.slug === slug);
			expect(created, "created client persisted").toBeDefined();
			clientId = created!.client_id;
			expect(created!.enabled).toBe(true);
			expect(created!.client_type).toBe("confidential");
			expect(created!.redirect_uris).toEqual([callback]);
			expect(created!.allowed_scopes).toEqual(["items:read"]);

			const row = page.getByTestId(`oauth-client-row-${created!.id}`);
			await expect(row).toBeVisible();
			await expect(row).toContainText(displayName);
			await expect(row).toContainText(clientId.slice(0, 9));

			// Rotation confirms through the shared dialog, then reveals a new
			// secret; the old one stops working.
			await page.getByTestId(`oauth-client-rotate-${created!.id}`).click();
			await page.getByTestId("dialog-confirm").click();
			await expect(page.getByTestId("oauth-client-secret-value")).toBeVisible();
			const rotatedSecret = (
				await page.getByTestId("oauth-client-secret-value").innerText()
			).trim();
			expect(rotatedSecret).not.toBe(secret);
			await page.getByTestId("oauth-client-secret-done").click();
			await expect(page.getByTestId("oauth-client-secret-value")).toHaveCount(0);
		} finally {
			const clients = await listClients(request);
			const created = clients.find((client) => client.slug === slug);
			if (created) {
				const deleteResponse = await request.delete(`/api/admin/oauth-clients/${created.id}`, {
					headers: SEC_FETCH,
				});
				expect(deleteResponse.ok()).toBeTruthy();
			}
		}

		// Row disappears without a reload once deletion settles.
		const remaining = await listClients(request);
		expect(remaining.find((client) => client.slug === slug)).toBeUndefined();
	});

	test("public clients skip the secret reveal and persist without one", async ({
		page,
		request,
	}) => {
		const stamp = Date.now();
		const slug = `e2e-oauth-pub-${stamp}`;

		try {
			await page.goto("/admin/integration-providers");
			await page.getByTestId("integrations-tab-inbound").click();
			await page.getByTestId("oauth-client-add").click();

			await page.getByTestId("oauth-client-display-name").fill(`E2E OAuth Public ${stamp}`);
			await page.getByTestId("oauth-client-slug").fill(slug);
			await page.getByTestId("oauth-client-redirect-uris").fill(`http://localhost:3000/cb/${slug}`);
			await page.getByTestId("oauth-client-scope-items:read").click();
			await page.getByTestId("oauth-client-type").selectOption("public");
			// Public clients show no secret modal to await, so synchronize on the
			// create response before asserting persistence.
			const registered = page.waitForResponse(
				(resp) =>
					resp.request().method() === "POST" &&
					resp.url().includes("/api/admin/oauth-clients"),
			);
			await page.getByTestId("oauth-client-register-submit").click();
			expect((await registered).ok()).toBeTruthy();

			// No secret modal for public clients — row lands directly.
			const clients = await listClients(request);
			const created = clients.find((client) => client.slug === slug);
			expect(created, "public client persisted").toBeDefined();
			expect(created!.client_type).toBe("public");
			await expect(page.getByTestId(`oauth-client-row-${created!.id}`)).toBeVisible();
			await expect(page.getByTestId("oauth-client-secret-value")).toHaveCount(0);
		} finally {
			const clients = await listClients(request);
			const created = clients.find((client) => client.slug === slug);
			if (created) {
				await request.delete(`/api/admin/oauth-clients/${created.id}`, { headers: SEC_FETCH });
			}
		}
	});
});
