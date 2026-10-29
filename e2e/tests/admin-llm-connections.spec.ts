import { expect, type APIRequestContext, test } from "../fixtures/context-path";

/**
 * Admin LLM connection manager (/admin/llm-connections). Drives the create,
 * edit, and delete modals against the real provider catalog and verifies
 * every mutation against the admin API.
 */

const SEC_FETCH = { "Sec-Fetch-Site": "same-origin" };

interface LlmConnection {
	id: number;
	name: string;
	provider_type: string;
	model: string;
	base_url: string;
	is_enabled: boolean;
}

async function listConnections(request: APIRequestContext): Promise<LlmConnection[]> {
	const response = await request.get("/api/admin/llm-connections", { headers: SEC_FETCH });
	expect(response.ok()).toBeTruthy();
	return await response.json();
}

test.describe("Admin LLM connections", () => {
	test("creates, edits, and deletes a connection through the admin UI", async ({
		page,
		request,
	}) => {
		const stamp = Date.now();
		const name = `E2E LLM ${stamp}`;
		const renamed = `E2E LLM Renamed ${stamp}`;
		let createdId = 0;

		// The provider catalog drives the form's provider/model selects.
		const providersResponse = await request.get("/api/llm/providers", { headers: SEC_FETCH });
		expect(providersResponse.ok()).toBeTruthy();
		const providers = (await providersResponse.json()) as Array<{ type: string }>;
		expect(
			providers.some((entry) => entry.type === "anthropic"),
			"anthropic provider exists in the catalog",
		).toBe(true);

		try {
			await page.goto("/admin/llm-connections");
			await page.locator("#llm-connection-add").click();

			const dialog = page.locator('[role="dialog"]');
			await expect(dialog).toBeVisible();
			await page.locator("#llm-connection-name").fill(name);
			await page.locator("#llm-connection-provider").click();
			await page
				.locator('[data-testid="llm-connection-provider-option"][data-option-id="anthropic"]')
				.click();
		await page.locator("#llm-connection-api-key").fill(`sk-e2e-${stamp}`);
			// The model picker lists the provider's cached catalog: open it and
			// pick a curated model row by its option id.
			await page.locator("#llm-connection-model").click();
			await page
				.getByTestId("picker-option-list")
				.locator('[data-option-id="claude-haiku-4-5-20251001"]')
				.click();
			// Synchronize on the create response before listing; the admin list is
			// not guaranteed to have refreshed when the click resolves.
			const createdResponse = page.waitForResponse(
				(response) =>
					response.request().method() === "POST" &&
					response.url().includes("/api/admin/llm-connections"),
			);
			await page.getByTestId("entity-form-confirm").click();
			expect((await createdResponse).ok()).toBeTruthy();

			const created = (await listConnections(request)).find((conn) => conn.name === name);
			expect(created, "created connection persisted").toBeDefined();
			createdId = created!.id;
			expect(created!.provider_type).toBe("anthropic");
			expect(created!.model).toBe("claude-haiku-4-5-20251001");

			const row = page.getByTestId(`llm-connection-row-${createdId}`);
			await expect(row).toBeVisible();

			// Rename through the edit modal; the stored API key stays configured.
			await row.getByTestId("llm-connection-edit").click();
			await expect(dialog).toBeVisible();
			await page.locator("#llm-connection-name").fill(renamed);
			await page.getByTestId("entity-form-confirm").click();

			await expect
				.poll(async () =>
					listConnections(request).then(
						(connections) =>
							connections.find((conn) => conn.id === createdId)?.name ?? "",
					),
				)
				.toBe(renamed);
			await expect(page.getByTestId(`llm-connection-row-${createdId}`)).toContainText(renamed);

			// Delete through the row action and confirm.
			await page.getByTestId(`llm-connection-row-${createdId}`).getByTestId("llm-connection-delete").click();
			await expect(page.getByTestId("dialog-confirm")).toBeVisible();
			await page.getByTestId("dialog-confirm").click();
			await expect(page.getByTestId(`llm-connection-row-${createdId}`)).toHaveCount(0);
			await expect
				.poll(async () =>
					listConnections(request).then(
						(connections) => !connections.some((conn) => conn.id === createdId),
					),
				)
				.toBe(true);
			createdId = 0;
		} finally {
			if (createdId) {
				await request.delete(`/api/admin/llm-connections/${createdId}`, { headers: SEC_FETCH });
			}
		}
	});
});
