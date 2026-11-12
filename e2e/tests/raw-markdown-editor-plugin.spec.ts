import { randomUUID } from "node:crypto";
import {
	authenticateAdminRequest,
	createItemViaAPI,
	createWorkspaceViaAPI,
} from "../fixtures/api-helpers";
import { expect, externalPath, test } from "../fixtures/context-path";
import {
	deleteCapabilityPlugins,
	installEditorPluginFromCore,
} from "../fixtures/plugin-zip";
import { generateItem, generateWorkspace } from "../fixtures/test-data";

const PLUGIN_NAME = "markdown-raw-editor";
// Extension id "raw-markdown" on plugin "markdown-raw-editor".
const PROVIDER_TESTID = "plugin-editor-provider-raw-markdown";

// Constructs Milkdown's serializer rewrites: underscore emphasis, tilde fences,
// and star bullets. The raw editor must preserve them byte for byte.
const RAW_MARKDOWN = [
	"# Raw source",
	"",
	"_underscore emphasis_ stays put.",
	"",
	"~~~text",
	"fenced with tildes",
	"~~~",
	"",
	"* star bullet",
	"",
].join("\n");

test("the raw markdown editor round-trips exact source through the item description", async ({
	page,
	request,
}) => {
	test.setTimeout(90_000);
	const coreDir = process.env.E2E_CORE_DIR;
	expect(coreDir, "E2E_CORE_DIR is set by run-e2e.sh").toBeTruthy();

	const suffix = `rawmd${Date.now()}${randomUUID().slice(0, 4)}`;
	try {
		await authenticateAdminRequest(request);
		await deleteCapabilityPlugins(request, PLUGIN_NAME);
		await installEditorPluginFromCore(request, coreDir as string, PLUGIN_NAME);

		const workspace = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
		const itemData = generateItem(workspace.id, suffix);
		const item = await createItemViaAPI(request, workspace.id, {
			title: itemData.title,
			description: RAW_MARKDOWN,
		});

		await page.goto(`/workspaces/${workspace.id}/items/${item.id}`);
		const display = page.getByTestId("item-description-display");
		await expect(display).toBeVisible({ timeout: 15_000 });
		await display.click();

		const provider = page.getByTestId(PROVIDER_TESTID);
		await expect(provider).toBeVisible({ timeout: 15_000 });
		await provider.click();

		const frame = page.frameLocator('[data-testid="plugin-editor-frame"]');
		const textarea = frame.getByTestId("raw-markdown-textarea");
		await expect(textarea).toBeVisible({ timeout: 15_000 });
		// The host spinner must clear once the ready handshake completes.
		await expect(page.getByTestId("plugin-editor-loading")).toHaveCount(0);
		// The WYSIWYG serializer would have normalized this source; the raw
		// editor must show exactly what the API stored.
		await expect(textarea).toHaveValue(RAW_MARKDOWN);
		// The plugin must not add a second save path; the surface owns Save.
		await expect(frame.getByTestId("raw-markdown-save")).toHaveCount(0);

		const edited = `${RAW_MARKDOWN}\nEdited in raw mode.\n`;
		await textarea.fill(edited);

		// Live sync mirrors the edit into the host's content; the surface's own
		// Save button persists it through the normal item API.
		await page.getByTestId("item-description-save").click();

		await expect
			.poll(
				async () => {
					const response = await request.get(externalPath(`/api/v2/items/${item.id}`));
					const body = await response.json();
					return body.data?.description;
				},
				{ timeout: 15_000 },
			)
			.toBe(edited);
	} finally {
		await deleteCapabilityPlugins(request, PLUGIN_NAME);
	}
});
