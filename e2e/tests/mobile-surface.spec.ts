import type { Request } from "@playwright/test";
import {
	createItemViaAPI,
	createWorkspaceViaAPI,
} from "../fixtures/api-helpers";
import {
	type APIRequestContext,
	expect,
	test,
} from "../fixtures/context-path";
import { generateItem, generateWorkspace } from "../fixtures/test-data";

const SEC_FETCH = { "Sec-Fetch-Site": "same-origin" };

async function configureOptionalCreateFields(
	request: APIRequestContext,
	workspaceId: number,
	suffix: string,
) {
	const itemTypesResponse = await request.get("/api/v2/item-types", {
		headers: SEC_FETCH,
	});
	expect(itemTypesResponse.ok()).toBeTruthy();
	const itemTypes = (await itemTypesResponse.json()).data;
	const itemTypeId = itemTypes[0]?.id;
	expect(itemTypeId).toBeGreaterThan(0);

	const screenResponse = await request.post("/api/screens", {
		headers: SEC_FETCH,
		data: { name: `Mobile optional fields ${suffix}` },
	});
	expect(screenResponse.ok()).toBeTruthy();
	const screen = await screenResponse.json();

	const fieldsResponse = await request.put(`/api/screens/${screen.id}/fields`, {
		headers: SEC_FETCH,
		data: ["priority", "assignee", "due_date", "start_date", "end_date"].map(
			(field_identifier, display_order) => ({
				field_type: "system",
				field_identifier,
				display_order,
				is_required: false,
				field_width: "full",
			}),
		),
	});
	expect(fieldsResponse.ok()).toBeTruthy();

	const configurationResponse = await request.post("/api/configuration-sets", {
		headers: SEC_FETCH,
		data: {
			name: `Mobile optional config ${suffix}`,
			workspace_ids: [workspaceId],
			create_screen_id: screen.id,
			edit_screen_id: screen.id,
			view_screen_id: screen.id,
			item_type_configs: [
				{
					item_type_id: itemTypeId,
					create_screen_id: screen.id,
					edit_screen_id: screen.id,
					view_screen_id: screen.id,
				},
			],
		},
	});
	expect(configurationResponse.ok()).toBeTruthy();
	const configuration = await configurationResponse.json();

	return { configurationId: configuration.id, screenId: screen.id };
}

/**
 * Mobile PWA surface (/m/*). Exercises the phone shell at an iPhone-ish
 * viewport: bottom-nav tab switching across the four data-backed views, the
 * installable manifest, and the item-detail route. The shell is route-driven
 * (renders for any /m/* path regardless of viewport), so the small viewport is
 * realism rather than a gate.
 */

test.use({ viewport: { width: 390, height: 844 }, hasTouch: true });

test.describe("Mobile surface", () => {
	test("bottom nav switches between the four views", {
		tag: "@critical-browser",
	}, async ({ page }) => {
		await page.goto("/m");


		await expect(page.getByTestId("mobile-shell")).toBeVisible();
		await expect(page.getByTestId("mobile-nav")).toBeVisible();

		// My Work is the default tab — header + the three segments render.
		await expect(page.getByTestId("mobile-header-title")).toHaveText("My Work");
		await expect(page.getByTestId("my-work-segment-assigned")).toBeVisible();
		await expect(page.getByTestId("my-work-segment-watched")).toBeVisible();
		await expect(page.getByTestId("my-work-segment-recent")).toBeVisible();

		await page.getByTestId("mobile-nav-personal").click();
		await expect(page.getByTestId("mobile-header-title")).toHaveText(
			"Personal",
		);

		await page.getByTestId("mobile-nav-timer").click();
		await expect(page.getByTestId("mobile-header-title")).toHaveText("Timer");
		await expect(page.getByTestId("timer-card")).toBeVisible();

		await page.getByTestId("mobile-nav-notifications").click();
		await expect(page.getByTestId("mobile-header-title")).toHaveText(
			"Notifications",
		);
		await expect(page.getByTestId("notifications-list")).toBeVisible();

		await page.getByTestId("mobile-nav-my-work").click();
		await expect(page.getByTestId("mobile-header-title")).toHaveText("My Work");
	});

	test("My Work segments each render a list or empty state", async ({
		page,
	}) => {
		await page.goto("/m");


		for (const seg of ["assigned", "watched", "recent"]) {
			await page.getByTestId(`my-work-segment-${seg}`).click();
			// Either rows or the segment's empty message — both live under the list.
			await expect(page.getByTestId("my-work-list")).toBeVisible();
			const rows = page.getByTestId("mobile-item-row");
			const empty = page.getByTestId("my-work-empty");
			await expect
				.poll(
					async () =>
						(await rows.count()) > 0 ||
						(await empty.isVisible().catch(() => false)),
				)
				.toBeTruthy();
		}
	});

	test("tapping a work item opens the mobile detail", async ({
		page,
		request,
	}) => {
		const meResponse = await request.get("/api/auth/me", {
			headers: SEC_FETCH,
		});
		expect(meResponse.ok()).toBeTruthy();
		const me = await meResponse.json();
		const currentUserId = me.user?.id ?? me.id;
		expect(currentUserId).toBeGreaterThan(0);

		const workspaceData = generateWorkspace("mobile-detail");
		const workspace = await createWorkspaceViaAPI(request, workspaceData);
		const itemData = generateItem(workspace.id, "mobile-detail");
		const item = await createItemViaAPI(request, workspace.id, {
			title: itemData.title,
			description: itemData.description,
			assignee_id: currentUserId,
		});

		await page.goto("/m");


		const row = page.locator(`#mobile-item-row-${item.id}`);
		await expect(row).toBeVisible();
		await row.click();
		await expect(page).toHaveURL(new RegExp(`/m/items/${item.id}$`));
		await expect(page.getByTestId("mobile-item-detail")).toBeVisible();
		await expect(page.getByTestId("detail-title")).toBeVisible();

		// Back returns to the shell.
		await page.getByTestId("mobile-header-back").click();
		await expect(page.getByTestId("mobile-nav")).toBeVisible();
	});

	test("serves an installable web manifest", async ({ page }) => {
		const res = await page.goto("/manifest.webmanifest");
		if (!res) throw new Error("manifest navigation did not return a response");
		expect(res.ok()).toBeTruthy();
		expect(res.headers()["content-type"]).toContain(
			"application/manifest+json",
		);
		const manifest = await res.json();
		expect(manifest.start_url).toContain("m");
		expect(manifest.display).toBe("standalone");
		expect(manifest.display_override ?? []).not.toContain(
			"window-controls-overlay",
		);
	});

	test("search opens from My Work and queries items", async ({ page }) => {
		await page.goto("/m");


		await page.getByTestId("mobile-search-open").click();
		await expect(page).toHaveURL(/\/m\/search$/);
		const input = page.getByTestId("mobile-search-input");
		await expect(input).toBeVisible();
		// Before typing, the prompt shows; after typing, results or empty resolve.
		await expect(page.getByTestId("search-prompt")).toBeVisible();
		await input.fill("zzz-no-such-item-xyz");
		await expect
			.poll(
				async () =>
					(await page.getByTestId("mobile-item-row").count()) > 0 ||
					(await page
						.getByTestId("search-empty")
						.isVisible()
						.catch(() => false)),
			)
			.toBeTruthy();
	});

	test("create FAB opens the create page and can create an item", async ({
		page,
		request,
	}) => {
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace("mobile-create"),
		);
		const item = generateItem(workspace.id, "mobile-create");

		await page.goto("/m");


		// The FAB navigates to the dedicated create route (WI-1327) — a full
		// page, not a dialog.
		await page.getByTestId("mobile-create-fab").click();
		await expect(page).toHaveURL(/\/m\/new$/);
		await expect(page.getByTestId("mobile-create-page")).toBeVisible();
		await expect(page.getByTestId("editor-title")).toHaveText("New item");
		await expect(page.getByTestId("create-title")).toBeVisible();

		await page
			.getByTestId("create-workspace")
			.selectOption(String(workspace.id));
		await page.getByTestId("create-title").fill(item.title);
		await expect(page.getByTestId("create-type")).not.toHaveValue("");
		await page.getByTestId("editor-save").click();
		await expect(page).toHaveURL(/\/m\/items\/\d+/);
		await expect(page.getByTestId("detail-title")).toHaveText(item.title);
	});

	test("expanded optional fields still allow creating an item", async ({
		page,
		request,
	}) => {
		const suffix = `${Date.now()}-${Math.floor(Math.random() * 10000)}`;
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace(`mobile-optional-${suffix}`),
		);
		const item = generateItem(workspace.id, `mobile-optional-${suffix}`);
		const configuration = await configureOptionalCreateFields(
			request,
			workspace.id,
			suffix,
		);

		try {
			await page.goto("/m");

			await page.getByTestId("mobile-create-fab").click();
			await page
				.getByTestId("create-workspace")
				.selectOption(String(workspace.id));
			await page.getByTestId("create-title").fill(item.title);

			// Regression coverage for WI-632: every configured field must remain
			// reachable and the create must succeed end to end. Since WI-1327 the
			// system properties live in the footer chip bar instead of the optional
			// section — the bar shows them without any expansion step.
			const propertiesBar = page.getByTestId("create-properties");
			await expect(propertiesBar).toBeVisible();
			const dueDate = page.getByTestId("configured-system-end_date");
			await expect(dueDate).toBeVisible();
			await dueDate.locator("input").fill("2030-03-30");

			await page.getByTestId("editor-save").click();
			await expect(page).toHaveURL(/\/m\/items\/\d+/);
			await expect(page.getByTestId("detail-title")).toHaveText(item.title);
		} finally {
			await request
				.delete(`/api/configuration-sets/${configuration.configurationId}`, {
					headers: SEC_FETCH,
				})
				.catch(() => {});
			await request
				.delete(`/api/screens/${configuration.screenId}`, {
					headers: SEC_FETCH,
				})
				.catch(() => {});
		}
	});

	test("status sheet changes an item's status from the detail", async ({
		page,
		request,
	}) => {
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace("mobile-status-sheet"),
		);
		const itemData = generateItem(workspace.id, "mobile-status-sheet");
		const item = await createItemViaAPI(request, workspace.id, {
			title: itemData.title,
		});

		await page.goto(`/m/items/${item.id}`);
		await expect(page.getByTestId("mobile-item-detail")).toBeVisible();

		const currentStatus = (
			await page.getByTestId("detail-status").innerText()
		).trim();
		await page.getByTestId("status-picker-trigger").click();

		const sheet = page.getByTestId("status-sheet");
		await expect(sheet).toBeVisible();
		const options = sheet.locator('[role="option"]');
		const count = await options.count();
		expect(count).toBeGreaterThan(0);

		// Pick the first transition whose status differs from the current one.
		let target = null;
		for (let i = 0; i < count; i++) {
			const name = (await options.nth(i).innerText()).trim();
			if (name && name !== currentStatus) {
				target = name;
				await options.nth(i).click();
				break;
			}
		}
		expect(target).not.toBeNull();

		await expect(sheet).not.toBeVisible();
		await expect(page.getByTestId("detail-status")).toContainText(target);

		// The change persists.
		await page.reload();
		await expect(page.getByTestId("detail-status")).toContainText(target);
	});

	test("assignee sheet assigns and unassigns a user", async ({
		page,
		request,
	}) => {
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace("mobile-assignee-sheet"),
		);
		const itemData = generateItem(workspace.id, "mobile-assignee-sheet");
		const item = await createItemViaAPI(request, workspace.id, {
			title: itemData.title,
		});

		await page.goto(`/m/items/${item.id}`);
		await expect(page.getByTestId("mobile-item-detail")).toBeVisible();
		await expect(page.getByTestId("assignee-picker-trigger")).toContainText(
			"Unassigned",
		);

		await page.getByTestId("assignee-picker-trigger").click();
		const sheet = page.getByTestId("assignee-sheet");
		await expect(sheet).toBeVisible();
		const options = sheet.locator('[role="option"]');
		await expect(options.first()).toBeVisible();
		await options.first().click();

		await expect(sheet).not.toBeVisible();
		await expect(page.getByTestId("assignee-picker-trigger")).not.toContainText(
			"Unassigned",
		);

		// Clearing returns to Unassigned and persists.
		await page.getByTestId("assignee-picker-trigger").click();
		await expect(page.getByTestId("mobile-sheet-clear")).toBeVisible();
		await page.getByTestId("mobile-sheet-clear").click();
		await expect(page.getByTestId("assignee-picker-trigger")).toContainText(
			"Unassigned",
		);
		await page.reload();
		await expect(page.getByTestId("assignee-picker-trigger")).toContainText(
			"Unassigned",
		);
	});

	test("edit page updates title and description", async ({
		page,
		request,
	}) => {
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace("mobile-item-edit"),
		);
		const itemData = generateItem(workspace.id, "mobile-item-edit");
		const item = await createItemViaAPI(request, workspace.id, {
			title: itemData.title,
			description: itemData.description,
		});

		await page.goto(`/m/items/${item.id}`);
		await expect(page.getByTestId("detail-title")).toHaveText(itemData.title);

		// The detail was read-only before WI-1327 — the Edit affordance is the
		// new path to change title and description on the phone.
		await page.getByTestId("detail-edit").click();
		await expect(page).toHaveURL(new RegExp(`/m/items/${item.id}/edit$`));
		await expect(page.getByTestId("mobile-item-edit-page")).toBeVisible();
		await expect(page.getByTestId("item-edit-title")).toHaveValue(
			itemData.title,
		);

		const newTitle = `${itemData.title} (edited)`;
		const newDescription = "Updated from the PWA.";
		await page.getByTestId("item-edit-title").fill(newTitle);
		await page.getByTestId("item-edit-description").fill(newDescription);
		await page.getByTestId("editor-save").click();

		await expect(page).toHaveURL(new RegExp(`/m/items/${item.id}$`));
		await expect(page.getByTestId("detail-title")).toHaveText(newTitle);
		await expect(page.getByTestId("detail-description")).toContainText(
			newDescription,
		);

		await page.reload();
		await expect(page.getByTestId("detail-title")).toHaveText(newTitle);
	});

	test("editor cancel with unsaved changes asks before discarding", async ({
		page,
		request,
	}) => {
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace("mobile-edit-guard"),
		);
		const itemData = generateItem(workspace.id, "mobile-edit-guard");
		const item = await createItemViaAPI(request, workspace.id, {
			title: itemData.title,
		});

		await page.goto(`/m/items/${item.id}/edit`);
		await expect(page.getByTestId("item-edit-title")).toHaveValue(
			itemData.title,
		);

		await page.getByTestId("item-edit-title").fill("Changed but unsaved");
		await page.getByTestId("editor-cancel").click();

		const sheet = page.getByTestId("item-edit-discard-sheet");
		await expect(sheet).toBeVisible();

		// "Keep editing" stays on the form with the draft intact.
		await page.getByTestId("mobile-confirm-cancel").click();
		await expect(sheet).not.toBeVisible();
		await expect(page.getByTestId("item-edit-title")).toHaveValue(
			"Changed but unsaved",
		);

		// Discard leaves without saving.
		await page.getByTestId("editor-cancel").click();
		await page.getByTestId("mobile-confirm-accept").click();
		await expect(page.getByTestId("mobile-item-detail")).toBeVisible();
		await expect(page.getByTestId("detail-title")).toHaveText(itemData.title);
	});

	test("editor back gesture with unsaved changes is vetoed by the discard guard", async ({
		page,
		request,
	}) => {
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace("mobile-edit-backguard"),
		);
		const itemData = generateItem(workspace.id, "mobile-edit-backguard");
		const item = await createItemViaAPI(request, workspace.id, {
			title: itemData.title,
		});

		await page.goto(`/m/items/${item.id}`);
		await expect(page.getByTestId("detail-title")).toHaveText(itemData.title);

		// Enter the editor through the app: the back gesture needs an in-app
		// entry to pop (a deep-linked first entry would leave the document).
		await page.getByTestId("detail-edit").click();
		await expect(page).toHaveURL(new RegExp(`/m/items/${item.id}/edit$`));
		await expect(page.getByTestId("item-edit-title")).toHaveValue(
			itemData.title,
		);

		await page.getByTestId("item-edit-title").fill("Back gesture draft");
		await page.goBack();

		// The pop is vetoed: still on the editor, discard sheet instead.
		await expect(page).toHaveURL(new RegExp(`/m/items/${item.id}/edit$`));
		const sheet = page.getByTestId("item-edit-discard-sheet");
		await expect(sheet).toBeVisible();

		// Keeping the edit restores the form; discarding then leaves cleanly.
		await page.getByTestId("mobile-confirm-cancel").click();
		await expect(sheet).not.toBeVisible();
		await expect(page).toHaveURL(new RegExp(`/m/items/${item.id}/edit$`));

		await page.getByTestId("editor-cancel").click();
		await page.getByTestId("mobile-confirm-accept").click();
		await expect(page).toHaveURL(new RegExp(`/m/items/${item.id}$`));
		await expect(page.getByTestId("mobile-item-detail")).toBeVisible();
		await expect(page.getByTestId("detail-title")).toHaveText(itemData.title);
	});

	test("editor drafts survive a reload and clear on discard", async ({
		page,
		request,
	}) => {
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace("mobile-edit-draft"),
		);
		const itemData = generateItem(workspace.id, "mobile-edit-draft");
		const item = await createItemViaAPI(request, workspace.id, {
			title: itemData.title,
		});

		await page.goto(`/m/items/${item.id}/edit`);
		await expect(page.getByTestId("item-edit-title")).toHaveValue(
			itemData.title,
		);
		await page.getByTestId("item-edit-title").fill("Drafted before reload");

		// sessionStorage keeps the draft across a reload (or app kill).
		await page.reload();
		await expect(page.getByTestId("item-edit-title")).toHaveValue(
			"Drafted before reload",
		);

		// Discarding drops the draft: a fresh editor shows the server values.
		await page.getByTestId("editor-cancel").click();
		await page.getByTestId("mobile-confirm-accept").click();
		await expect(page.getByTestId("mobile-item-detail")).toBeVisible();
		await page.goto(`/m/items/${item.id}/edit`);
		await expect(page.getByTestId("item-edit-title")).toHaveValue(
			itemData.title,
		);
	});

	test("create and edit inputs are zoom-safe (font-size >= 16px)", async ({
		page,
		request,
	}) => {
		// WI-1325: iOS zooms the page when a focused input's font-size is below
		// 16px. The mobile surface floors every form control via app.css.
		await page.goto("/m/new");
		await expect(page.getByTestId("mobile-create-page")).toBeVisible();
		const createTitleFontSize = await page
			.getByTestId("create-title")
			.evaluate((element) => parseFloat(getComputedStyle(element).fontSize));
		expect(createTitleFontSize).toBeGreaterThanOrEqual(16);

		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace("mobile-zoom-safe"),
		);
		const itemData = generateItem(workspace.id, "mobile-zoom-safe");
		const item = await createItemViaAPI(request, workspace.id, {
			title: itemData.title,
		});

		await page.goto(`/m/items/${item.id}/edit`);
		await expect(page.getByTestId("item-edit-title")).toBeVisible();
		const editTitleFontSize = await page
			.getByTestId("item-edit-title")
			.evaluate((element) => parseFloat(getComputedStyle(element).fontSize));
		expect(editTitleFontSize).toBeGreaterThanOrEqual(16);
		const editDescriptionFontSize = await page
			.getByTestId("item-edit-description")
			.evaluate((element) => parseFloat(getComputedStyle(element).fontSize));
		expect(editDescriptionFontSize).toBeGreaterThanOrEqual(16);
	});

	test("personal checklist embeds the desktop todo list with completed filter", async ({
		page,
		request,
	}) => {
		// The Personal tab now embeds the desktop TodoList instead of a parallel
		// mobile checklist: completed tasks stay visible (default last-7-days
		// history), the range filter hides them, checking off persists, and rows
		// clamp to the viewport (long unbroken titles used to widen the page).
		const personalResponse = await request.get("/api/workspaces/personal", {
			headers: SEC_FETCH,
		});
		expect(personalResponse.ok()).toBeTruthy();
		const personalWorkspace = await personalResponse.json();

		const stamp = `mobile-personal-${Date.now()}-${Math.random()
			.toString(36)
			.slice(2, 8)}`;
		const openTask = await createItemViaAPI(request, personalWorkspace.id, {
			title: `${stamp} still open`,
		});
		const doneTask = await createItemViaAPI(request, personalWorkspace.id, {
			title: `${stamp} already done`,
		});
		// Complete the second task through the production transition endpoint.
		const doneResponse = await request.post(
			`/api/v2/items/${doneTask.id}/transition`,
			{
				headers: SEC_FETCH,
				data: { to_status_id: 3 },
			},
		);
		expect(
			doneResponse.ok(),
			`transition to done failed (${doneResponse.status()})`,
		).toBeTruthy();
		// A single unbroken token must not widen the page (WI-1331 clamp).
		const longTitle = `${stamp} ${"Supercalifragilisticexpialidocious".repeat(8)}`;
		const longTask = await createItemViaAPI(request, personalWorkspace.id, {
			title: longTitle.slice(0, 255),
		});

		try {
			await page.goto("/m/personal");
			await expect(page.getByTestId("todo-personal-section")).toBeVisible();

			const openRow = page
				.getByTestId("todo-personal-row")
				.filter({ hasText: `${stamp} still open` });
			const doneRow = page
				.getByTestId("todo-personal-row")
				.filter({ hasText: `${stamp} already done` });
			const longRow = page
				.getByTestId("todo-personal-row")
				.filter({ hasText: "Supercalifragilistic" });

			// Desktop parity: completed tasks stay listed with a checked box.
			await expect(openRow).toBeVisible();
			await expect(doneRow).toBeVisible();
			await expect(
				doneRow
					.getByTestId("todo-personal-checkbox")
					.locator(".checkbox-box"),
			).toHaveClass(/checked/);

			// The completed-history filter starts collapsed on the phone; expanding
			// it reveals the desktop range controls: "None" hides completed tasks
			// while genuinely open ones remain.
			await expect(page.getByTestId("todo-done-filter")).toBeHidden();
			await page.getByTestId("todo-filter-toggle").click();
			await expect(page.getByTestId("todo-done-filter")).toBeVisible();
			await page.getByTestId("done-range-none").click();
			await expect(doneRow).toHaveCount(0);
			await expect(openRow).toBeVisible();
			await page.getByTestId("done-range-7").click();
			await expect(doneRow).toBeVisible();

			// Checking the open task off keeps it listed but checked…
			await openRow.getByTestId("todo-personal-checkbox").click();
			await expect(
				openRow
					.getByTestId("todo-personal-checkbox")
					.locator(".checkbox-box"),
			).toHaveClass(/checked/);

			// …and the change persists on the server.
			await expect
				.poll(async () => {
					const stateResponse = await request.get(
						`/api/v2/items/${openTask.id}`,
						{ headers: SEC_FETCH },
					);
					expect(stateResponse.ok()).toBeTruthy();
					return ((await stateResponse.json()).data)?.status_id;
				})
				.toBe(3);

			// The list clamps to the viewport: no horizontal scrolling.
			await expect(longRow).toBeVisible();
			const clampsToViewport = await page
				.locator(".mobile-scroll")
				.evaluate(
					(el) =>
						(el as HTMLElement).scrollWidth <=
						(el as HTMLElement).clientWidth + 1,
				);
			expect(clampsToViewport).toBe(true);
		} finally {
			for (const id of [openTask.id, doneTask.id, longTask.id]) {
				await request
					.delete(`/api/v2/items/${id}`, { headers: SEC_FETCH })
					.catch(() => {});
			}
		}
	});

	test("long titles wrap fully visible in the item editor", async ({
		page,
		request,
	}) => {
		// WI-1331: a single-line <input> scrolls long titles out of view on a
		// phone; the editor field must wrap and grow with its content.
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace("mobile-title-wrap"),
		);
		// 255 is the server's title cap; ~2.5 phone lines at 1.35rem.
		const longTitle = (
			"A remarkably long mobile issue title that exceeds a single phone line "
		).repeat(3).slice(0, 250);
		const item = await createItemViaAPI(request, workspace.id, {
			title: longTitle,
		});

		await page.goto(`/m/items/${item.id}/edit`);
		const titleField = page.getByTestId("item-edit-title");
		await expect(titleField).toBeVisible();

		// A wrapping textarea, not a one-line input.
		expect(await titleField.evaluate((el) => el.tagName)).toBe("TEXTAREA");

		// It grows enough to show the whole title without internal scrolling.
		await expect
			.poll(() =>
				titleField.evaluate(
					(el) =>
						(el as HTMLElement).scrollHeight <=
						(el as HTMLElement).clientHeight + 1,
				),
			)
			.toBe(true);
	});

	test("page editor wraps the title and saves content with visible breaks", async ({
		page,
		request,
	}) => {
		// WI-1331: the mobile page editor keeps the borderless editor modality
		// (wrapping hero title, full-height content) and the save round trip
		// keeps `<br />` content rendering as line breaks in the reader.
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace("mobile-page-edit"),
		);
		const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
		const title = `Mobile page ${stamp}`;
		const content = "First line <br />second line\n\nThird paragraph";
		const createResponse = await request.post(
			`/api/v2/workspaces/${workspace.id}/pages`,
			{
				headers: SEC_FETCH,
				data: { title, content },
			},
		);
		expect(createResponse.ok()).toBeTruthy();
		const created = (await createResponse.json()).data;

		try {
			await page.goto(`/m/pages/${workspace.id}/${created.id}`);
			await expect(page.getByTestId("mobile-page-title")).toHaveText(title);

			await page.getByTestId("mobile-page-edit").click();
			await expect(page.getByTestId("mobile-page-editor")).toBeVisible();

			const titleField = page.getByTestId("mobile-page-title-input");
			expect(await titleField.evaluate((el) => el.tagName)).toBe("TEXTAREA");

			const newTitle = `${title} (edited)`;
			await titleField.fill(newTitle);
			await page.getByTestId("mobile-page-save").click();

			await expect(page.getByTestId("mobile-page-title")).toHaveText(
				newTitle,
			);
			// The saved content still renders the br spelling as a line break.
			await expect(page.getByTestId("mobile-page-content")).toBeVisible();
			await expect(
				page.getByTestId("mobile-page-content").locator("br").first(),
			).toBeAttached();
		} finally {
			await request
				.delete(`/api/v2/workspaces/${workspace.id}/pages/${created.id}`, {
					headers: SEC_FETCH,
				})
				.catch(() => {});
		}
	});

	test("page editor expands long content so nothing hides inside the field", async ({
		page,
		request,
	}) => {
		// The content field used a fixed height: a page longer than the field
		// overflowed inside an internally-scrolling textarea that phone gestures
		// could not reliably scroll, hiding half the page. The field must grow
		// with its content and leave scrolling to the outer view (WI-1331
		// modality: no field-internal scrollers in the mobile editors).
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace("mobile-page-grow"),
		);
		const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
		const title = `Mobile long page ${stamp}`;
		const content = Array.from(
			{ length: 30 },
			(_, i) =>
				`Paragraph ${i + 1} keeps typing so the page grows far past one phone screen.`,
		).join("\n\n");
		const createResponse = await request.post(
			`/api/v2/workspaces/${workspace.id}/pages`,
			{
				headers: SEC_FETCH,
				data: { title, content },
			},
		);
		expect(createResponse.ok()).toBeTruthy();
		const created = (await createResponse.json()).data;

		try {
			await page.goto(`/m/pages/${workspace.id}/${created.id}`);
			await expect(page.getByTestId("mobile-page-title")).toHaveText(title);

			await page.getByTestId("mobile-page-edit").click();
			await expect(page.getByTestId("mobile-page-editor")).toBeVisible();

			const contentField = page.getByTestId("mobile-page-content-input");
			// The field is tall enough to show all of its content — no internal
			// scrolling.
			await expect
				.poll(() =>
					contentField.evaluate(
						(el) =>
							(el as HTMLTextAreaElement).scrollHeight <=
							(el as HTMLTextAreaElement).clientHeight + 1,
					),
				)
				.toBe(true);

			// The outer view owns the scrolling: the expanded form is taller
			// than the screen, so the whole page is reachable through it.
			const outerScrolls = await page
				.locator(".mobile-scroll")
				.evaluate(
					(el) =>
						(el as HTMLElement).scrollHeight >
						(el as HTMLElement).clientHeight + 1,
				);
			expect(outerScrolls).toBe(true);
		} finally {
			await request
				.delete(`/api/v2/workspaces/${workspace.id}/pages/${created.id}`, {
					headers: SEC_FETCH,
				})
				.catch(() => {});
		}
	});

	test("item editor description grows with long content", async ({
		page,
		request,
	}) => {
		// Same contract as the page editor: a long description must expand its
		// field instead of hiding the tail inside an internally-scrolling box.
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace("mobile-desc-grow"),
		);
		const description = Array.from(
			{ length: 25 },
			(_, i) =>
				`Description paragraph ${i + 1} pushes the field past its old fixed height.`,
		).join("\n\n");
		const item = await createItemViaAPI(request, workspace.id, {
			title: `Long description item ${Date.now()}`,
			description,
		});

		try {
			await page.goto(`/m/items/${item.id}/edit`);
			const descriptionField = page.getByTestId("item-edit-description");
			await expect(descriptionField).toBeVisible();

			await expect
				.poll(() =>
					descriptionField.evaluate(
						(el) =>
							(el as HTMLTextAreaElement).scrollHeight <=
							(el as HTMLTextAreaElement).clientHeight + 1,
					),
				)
				.toBe(true);
		} finally {
			await request
				.delete(`/api/v2/items/${item.id}`, { headers: SEC_FETCH })
				.catch(() => {});
		}
	});

	test("batches page summaries across workspaces in bounded requests", async ({
		page,
		request,
	}) => {
		// WI-1918: the mobile pages list used to fetch each workspace with its
		// own request. It now sends one cross-workspace summaries request per
		// bounded batch and filters client-side.
		const suffix = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
		const wsA = await createWorkspaceViaAPI(
			request,
			generateWorkspace(`mob-pages-a-${suffix}`),
		);
		const wsB = await createWorkspaceViaAPI(
			request,
			generateWorkspace(`mob-pages-b-${suffix}`),
		);
		const titleA = `Alpha page ${suffix}`;
		const titleB = `Beta page ${suffix}`;
		const pageIds: number[] = [];
		for (const [workspace, title] of [
			[wsA, titleA],
			[wsB, titleB],
		] as const) {
			const created = await request.post(
				`/api/v2/workspaces/${workspace.id}/pages`,
				{ headers: SEC_FETCH, data: { title, content: "body" } },
			);
			expect(created.ok()).toBeTruthy();
			pageIds.push((await created.json()).data.id);
		}
		const [pageIdA, pageIdB] = pageIds;

		const summariesRequests: number[] = [];
		const perWorkspaceRequests: string[] = [];
		const onRequest = (browserRequest: Request) => {
			const url = new URL(browserRequest.url());
			if (url.pathname.endsWith("/pages/summaries")) {
				const ids = url.searchParams.get("workspace_ids");
				summariesRequests.push(ids ? ids.split(",").length : 0);
			} else if (
				browserRequest.method() === "GET" &&
				/\/workspaces\/\d+\/pages$/.test(url.pathname)
			) {
				perWorkspaceRequests.push(url.pathname);
			}
		};
		page.on("request", onRequest);

		try {
			await page.goto("/m/pages");
			await expect(page.getByTestId("mobile-pages-view")).toBeVisible();
			await expect(
				page.getByTestId(`mobile-pages-section-${wsA.id}`),
			).toBeVisible();
			await expect(
				page.getByTestId(`mobile-pages-section-${wsB.id}`),
			).toBeVisible();
			const rowA = page.getByTestId(`mobile-page-row-${pageIdA}`);
			const rowB = page.getByTestId(`mobile-page-row-${pageIdB}`);
			await expect(rowA).toBeVisible();
			await expect(rowA).toContainText(titleA);
			await expect(rowB).toBeVisible();
			await expect(rowB).toContainText(titleB);

			// Cross-workspace batching: at least one summaries request, each
			// covering no more than the 50-workspace server cap, and never a
			// request per workspace. The exact batch count depends on how many
			// workspaces exist in the shared database, so it is not asserted.
			expect(summariesRequests.length).toBeGreaterThanOrEqual(1);
			expect(perWorkspaceRequests).toHaveLength(0);
			for (const workspaceCount of summariesRequests) {
				expect(workspaceCount).toBeGreaterThan(0);
				expect(workspaceCount).toBeLessThanOrEqual(50);
			}

			// Filtering is client-side and must not issue another request.
			const requestsBeforeFilter = summariesRequests.length;
			await page.getByTestId("mobile-pages-filter").fill(titleA);
			await expect(rowA).toBeVisible();
			await expect(rowB).toHaveCount(0);
			expect(summariesRequests).toHaveLength(requestsBeforeFilter);
		} finally {
			page.off("request", onRequest);
			await request
				.delete(`/api/v2/workspaces/${wsA.id}`)
				.catch(() => {});
			await request
				.delete(`/api/v2/workspaces/${wsB.id}`)
				.catch(() => {});
		}
	});

	test("desktop item deep links redirect to the mobile surface", async ({
		page,
		request,
	}) => {
		// WI-1322: a phone following a desktop item URL (AI-chat link, push
		// notification) must land on the /m shell, not the desktop UI.
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace("mobile-deeplink"),
		);
		const itemData = generateItem(workspace.id, "mobile-deeplink");
		const item = await createItemViaAPI(request, workspace.id, {
			title: itemData.title,
		});

		await page.goto(`/workspaces/${workspace.id}/items/${item.id}`);
		await expect(page).toHaveURL(
			new RegExp(`/m/items/${item.id}$`),
		);
		await expect(page.getByTestId("mobile-item-detail")).toBeVisible();
		await expect(page.getByTestId("detail-title")).toHaveText(itemData.title);
	});
});
