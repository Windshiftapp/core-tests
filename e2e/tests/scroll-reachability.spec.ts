import {
	createCollectionViaAPI,
	createItemViaAPI,
	createUserViaAPI,
	createWorkspaceViaAPI,
} from "../fixtures/api-helpers";
import { expect, type Locator, test } from "../fixtures/context-path";
import { generateUser, generateWorkspace } from "../fixtures/test-data";
import { scrollToContent } from "../helpers/scroll-to-content";

test.describe.configure({ retries: 0 });

test.afterEach(async ({ page }, testInfo) => {
	if (testInfo.status === testInfo.expectedStatus || page.isClosed()) return;
	const layout = await page.evaluate(() =>
		Array.from(document.querySelectorAll<HTMLElement>("*"))
			.filter(
				(element) =>
					element.scrollHeight > element.clientHeight + 1 &&
					element.clientHeight > 0,
			)
			.map((element) => ({
				tag: element.tagName,
				id: element.id,
				testId: element.dataset.testid,
				className: element.className,
				overflow: getComputedStyle(element).overflowY,
				top: element.getBoundingClientRect().top,
				height: element.clientHeight,
				scrollHeight: element.scrollHeight,
				scrollTop: element.scrollTop,
			})),
	);
	await testInfo.attach("scroll-layout.json", {
		body: JSON.stringify(layout, null, 2),
		contentType: "application/json",
	});
});

// Floating pickers position asynchronously after the trigger's layout settles;
// a scroll gesture computed against the pre-positioning box lands elsewhere.
async function waitForStableBox(locator: Locator) {
	let previous: { x: number; y: number } | null = null;
	await expect
		.poll(async () => {
			const box = await locator.boundingBox();
			if (!box) return false;
			const stable =
				previous != null &&
				Math.abs(box.x - previous.x) < 1 &&
				Math.abs(box.y - previous.y) < 1;
			previous = box;
			return stable;
		})
		.toBe(true);
}

async function expectInsideViewport(locator: Locator) {
	await expect
		.poll(() =>
			locator.evaluate((element) => {
				const box = element.getBoundingClientRect();
				return box.top >= 0 && box.bottom <= window.innerHeight;
			}),
		)
		.toBe(true);
}

for (const height of [480, 320]) {
	test(`workspace menu reaches its final entry at ${height}px height`, async ({
		page,
		request,
	}) => {
		const workspaces: number[] = [];
		const prefix = generateWorkspace("scroll-menu").key;
		try {
			for (let index = 0; index < 12; index++) {
				const workspace = await createWorkspaceViaAPI(
					request,
					generateWorkspace(`${prefix}-${index}`),
				);
				workspaces.push(workspace.id);
			}
			await page.setViewportSize({ width: 1280, height });
			await page.goto("/");
			await page.getByTestId("workspace-breadcrumb-picker").click();
			await page.getByTestId("workspaces-search").fill(prefix);
			const menu = page.getByTestId("workspace-breadcrumb-picker-menu");
			const last = page.getByTestId("workspace-dropdown-item").last();
			await expect(last).toBeAttached();
			const target = await last.getAttribute("href");
			await menu.hover();
			await page.mouse.wheel(0, 3000);
			await expect(last).toBeInViewport();
			await expectInsideViewport(menu);
			await last.click();
			await expect(page).toHaveURL(new RegExp(`${target}/board$`));
		} finally {
			for (const id of workspaces) {
				expect(
					(await request.delete(`/api/v2/workspaces/${id}`)).status(),
				).toBe(204);
			}
		}
	});

	test(`field type picker reaches the last value at ${height}px height`, async ({
		page,
	}) => {
		await page.setViewportSize({ width: 1280, height });
		await page.goto("/admin/custom-fields");
		await page.locator("#create-field-button").click();
		const picker = page.locator("#field-type");
		await picker.click();
		const list = page.getByTestId("picker-option-list");
		await expect(list).toBeVisible();
		await list.hover();
		await page.mouse.wheel(0, 3000);
		const last = page.getByTestId("custom-field-type-linking");
		await expect(last).toBeInViewport();
		await expectInsideViewport(page.getByTestId("picker-dropdown"));
		await last.click();
		await expect(picker).toHaveValue("Linking");
	});

	test(`select reaches its final provider at ${height}px height`, async ({
		page,
	}) => {
		await page.setViewportSize({ width: 1280, height });
		await page.goto("/admin/llm-connections");
		await page.locator("#llm-connection-add").click();
		const trigger = page.locator("#llm-connection-provider");
		await trigger.click();
		const menu = page.getByTestId("llm-connection-provider-listbox");
		const last = page.getByTestId("llm-connection-provider-option").last();
		await expect(last).toBeAttached();
		const label = await last.innerText();
		await menu.press("End");
		await expect(last).toBeInViewport();
		await expectInsideViewport(menu);
		await menu.press("Enter");
		await expect(trigger).toHaveText(label.trim());
	});
}

test("admin sidebar reaches its last section without moving the page", async ({
	page,
}) => {
	await page.setViewportSize({ width: 1280, height: 480 });
	await page.goto("/admin/custom-fields");
	await expect(page.locator("#create-field-button")).toBeVisible();
	const last = page.getByTestId("admin-navigation-item").last();
	const target = await last.getAttribute("href");
	await expect(last).not.toBeInViewport();
	// The edge fades spell out that the nav continues past the fold.
	const bottomFade = page.getByTestId("admin-navigation-scroll-fade-bottom");
	await expect(bottomFade).toBeVisible();
	await expect(bottomFade).toContainText("Scroll down to see more");
	await expect(
		page.getByTestId("admin-navigation-scroll-fade-top"),
	).toBeHidden();
	await page.getByTestId("admin-navigation-scroll").hover();
	await page.mouse.wheel(0, 5000);
	await expect(last).toBeInViewport();
	await expect(bottomFade).toBeHidden();
	const topFade = page.getByTestId("admin-navigation-scroll-fade-top");
	await expect(topFade).toBeVisible();
	await expect(topFade).toContainText("Scroll up to see more");
	await expect(page.locator("#create-field-button")).toBeInViewport();
	await last.click();
	await expect(page).toHaveURL(new RegExp(`${target}$`));
});

test("admin sidebar drops the scroll affordance when the list fits", async ({
	page,
}) => {
	await page.setViewportSize({ width: 1280, height: 480 });
	await page.goto("/admin/custom-fields");
	await expect(
		page.getByTestId("admin-navigation-scroll-fade-bottom"),
	).toBeVisible();
	await page.locator("#admin-search").fill("Diagnostics");
	await expect(page.getByTestId("admin-navigation-item")).toHaveCount(1);
	await expect(
		page.getByTestId("admin-navigation-scroll-fade-bottom"),
	).toBeHidden();
	await expect(
		page.getByTestId("admin-navigation-scroll-fade-top"),
	).toBeHidden();
});

test("workspace settings content and navigation scroll independently", async ({
	page,
	request,
}) => {
	const workspace = await createWorkspaceViaAPI(
		request,
		generateWorkspace("scroll-settings"),
	);
	try {
		await page.setViewportSize({ width: 1280, height: 480 });
		await page.goto(`/workspaces/${workspace.id}/settings/general`);
		await expect(
			page.getByTestId("workspace-template-toggle-row"),
		).toBeAttached();
		// The settings sidebar and breadcrumb link mount once the routed
		// workspace hydrates. Their late layout shifts change the content
		// viewport, so wait for them before pinning any scroll position.
		await expect(page.getByTestId("workspace-admin-sidebar")).toBeAttached();
		await expect(
			page.getByTestId("workspace-breadcrumb-current"),
		).toBeVisible();
		await page.evaluate(() => document.fonts.ready);

		const save = page.getByTestId("workspace-settings-save");
		await expect(save).toBeAttached();
		await expect(save).not.toBeInViewport();
		const content = page.getByTestId("main-content-scroll");
		await content.hover();
		await page.mouse.wheel(0, 3000);
		await expect
			.poll(() =>
				content.evaluate(
					(element) =>
						element.scrollTop + element.clientHeight >=
						element.scrollHeight - 1,
				),
			)
			.toBe(true);
		await expect(save).toBeInViewport();
		// Move directly into the visible sidebar; locator.hover can scroll its ancestors.
		const navigation = page.getByTestId("workspace-admin-navigation-scroll");
		const bounds = await navigation.boundingBox();
		if (!bounds) throw new Error("Workspace navigation has no bounds");
		await page.mouse.move(
			bounds.x + bounds.width / 2,
			Math.max(bounds.y, 48) + 20,
		);
		await page.mouse.wheel(0, 3000);
		const danger = page.getByTestId("workspace-admin-nav-danger");
		await expect(danger).toBeInViewport();
		await expect(save).toBeInViewport();
		await danger.click();
		await expect(page.getByTestId("delete-workspace-open")).toBeVisible();
	} finally {
		expect(
			(await request.delete(`/api/v2/workspaces/${workspace.id}`)).status(),
		).toBe(204);
	}
});

test("API reference navigation reaches its final operation", async ({
	page,
}) => {
	await page.setViewportSize({ width: 1280, height: 480 });
	await page.goto("/api-docs");
	const links = page.getByTestId("api-docs-op-link");
	await expect(links.first()).toBeVisible();
	const last = links.last();
	const operationId = await last.getAttribute("data-op-id");
	if (!operationId) throw new Error("Final API operation has no ID");
	await page.getByTestId("api-docs-navigation-scroll").hover();
	await page.mouse.wheel(0, 100000);
	await expect(last).toBeInViewport();
	await last.click();
	await expect(page).toHaveURL(new RegExp(operationId));
	await expect(page.getByTestId("api-docs-filter")).toBeInViewport();
});

test("global navigation reaches the final main link on a short screen", async ({
	page,
}) => {
	await page.setViewportSize({ width: 1280, height: 480 });
	await page.goto("/");
	const teams = page.locator("#nav-teams");
	await expect(teams).toBeAttached();
	await page.getByTestId("main-navigation-scroll").hover();
	await page.mouse.wheel(0, 3000);
	await expect(teams).toBeInViewport();
	await teams.click();
	await expect(page).toHaveURL(/\/teams$/);
});

test("create modal workspace chip reaches its last option on a short screen", async ({
	page,
	request,
}) => {
	const workspaces: number[] = [];
	const prefix = generateWorkspace("scroll-chip").key;
	try {
		for (let index = 0; index < 12; index++) {
			workspaces.push(
				(
					await createWorkspaceViaAPI(
						request,
						generateWorkspace(`${prefix}-${index}`),
					)
				).id,
			);
		}
		await page.setViewportSize({ width: 1280, height: 320 });
		await page.goto(`/workspaces/${workspaces[0]}/board`);
		await page.locator("#global-create-button").click();
		await expect(page.locator("#work-item-title")).toBeFocused();
		const trigger = page.getByTestId("create-workspace-chip");
		await trigger.click();
		await page.getByTestId("create-workspace-chip-search").fill(prefix);
		const list = page.getByTestId("create-workspace-chip-listbox");
		const last = page.getByTestId("create-workspace-chip-option").last();
		await expect(last).toBeAttached();
		const key = (await last.innerText()).trim().split("\n").at(-1);
		if (!key) throw new Error("Final workspace option has no key");
		await list.hover();
		await page.mouse.wheel(0, 3000);
		await expect(last).toBeInViewport();
		await expectInsideViewport(
			page.getByTestId("create-workspace-chip-dropdown"),
		);
		await last.click();
		await expect(trigger).toHaveText(key);
	} finally {
		for (const id of workspaces)
			expect((await request.delete(`/api/v2/workspaces/${id}`)).status()).toBe(
				204,
			);
	}
});

test("collection field selector reaches its last field on a short screen", async ({
	page,
	request,
}) => {
	const collection = await createCollectionViaAPI(request, {
		name: generateWorkspace("scroll-filter").name,
		ql_query: 'status = "Open"',
	});
	try {
		await page.setViewportSize({ width: 1280, height: 480 });
		await page.goto(`/collections/${collection.id}`);
		await page.getByTestId("collection-add-dynamic-filter").click();
		await page.getByTestId("field-selector-trigger").click();
		const last = page.getByTestId(/^field-option-/).last();
		await expect(last).toBeAttached();
		const label = await last.innerText();
		// The catalog grows with custom fields created by other specs on the
		// shared server, so the list can be taller than one fixed wheel gesture.
		await scrollToContent(
			page,
			page.getByTestId("field-selector-scroll"),
			last,
		);
		await expect(last).toBeInViewport();
		await last.click();
		await expect(page.getByTestId("field-selector-value")).toHaveText(
			label.split("\n")[0],
		);
	} finally {
		expect(
			(await request.delete(`/api/v2/collections/${collection.id}`)).status(),
		).toBe(204);
	}
});

test("page label picker reaches the final label on a short screen", async ({
	page,
	request,
}) => {
	const workspace = await createWorkspaceViaAPI(
		request,
		generateWorkspace("scroll-labels"),
	);
	try {
		for (let index = 0; index < 20; index++) {
			const response = await request.post(
				`/api/v2/workspaces/${workspace.id}/page-labels`,
				{
					data: {
						name: `Label ${String(index).padStart(2, "0")}`,
						color: "#336699",
					},
				},
			);
			expect(response.ok(), await response.text()).toBeTruthy();
		}
		await page.setViewportSize({ width: 1280, height: 320 });
		await page.goto(`/workspaces/${workspace.id}/pages`);
		await page.getByTestId("pages-filter-trigger").click();
		const list = page.getByTestId("page-label-picker-list");
		const last = page.getByTestId("page-label-picker-row").last();
		await expect(last).toBeAttached();
		await list.hover();
		await page.mouse.wheel(0, 3000);
		await expect(last).toBeInViewport();
		await expectInsideViewport(page.getByTestId("page-label-picker"));
		await last.click();
		await expect(last).toHaveAttribute("aria-selected", "true");
		await page.keyboard.press("Escape");
		await expect(page.getByTestId("pages-filter-clear")).toBeVisible();
	} finally {
		expect(
			(await request.delete(`/api/v2/workspaces/${workspace.id}`)).status(),
		).toBe(204);
	}
});

test("column menu keeps its Apply action reachable on a short screen", async ({
	page,
	request,
}) => {
	const collection = await createCollectionViaAPI(request, {
		name: generateWorkspace("scroll-columns").name,
		ql_query: 'status = "Open"',
	});
	try {
		await page.setViewportSize({ width: 1280, height: 480 });
		await page.goto(`/collections/${collection.id}`);
		await page.getByTestId("collection-column-selector-trigger").click();
		await page.getByTestId("column-selector-scroll").hover();
		await page.mouse.wheel(0, 3000);
		const apply = page.getByTestId("column-selector-apply");
		await expect(apply).toBeInViewport();
		await expectInsideViewport(page.getByTestId("column-selector-menu"));
		await apply.click();
		await expect(page.getByTestId("column-selector-menu")).toBeHidden();
	} finally {
		expect(
			(await request.delete(`/api/v2/collections/${collection.id}`)).status(),
		).toBe(204);
	}
});

test("long page content reaches its last paragraph while the tree stays visible", async ({
	page,
	request,
}) => {
	const workspace = await createWorkspaceViaAPI(
		request,
		generateWorkspace("scroll-page-body"),
	);
	try {
		const response = await request.post(
			`/api/v2/workspaces/${workspace.id}/pages`,
			{
				data: {
					title: "Long page",
					content: `${"A paragraph of page content.\n\n".repeat(60)}Final paragraph.`,
				},
			},
		);
		expect(response.ok(), await response.text()).toBeTruthy();
		const created = (await response.json()).data;
		await page.setViewportSize({ width: 1280, height: 480 });
		await page.goto(`/workspaces/${workspace.id}/pages/${created.id}`);
		const last = page
			.getByTestId("page-editor")
			.getByText("Final paragraph.", { exact: true });
		await expect(last).toBeAttached();
		await expect(last).not.toBeInViewport();
		await page.getByTestId("pages-view").hover();
		await page.mouse.wheel(0, 10000);
		await expect(last).toBeInViewport();
		await expect(page.locator("#pages-add-button")).toBeInViewport();
	} finally {
		expect(
			(await request.delete(`/api/v2/workspaces/${workspace.id}`)).status(),
		).toBe(204);
	}
});

test("long custom-field modal reaches its submit action on a short screen", async ({
	page,
}) => {
	await page.setViewportSize({ width: 1280, height: 320 });
	await page.goto("/admin/custom-fields");
	await page.locator("#create-field-button").click();
	const save = page.getByTestId("custom-field-save");
	await expect(save).toBeAttached();
	await expect(save).not.toBeInViewport();
	const dialog = page.getByTestId("custom-field-dialog");
	// The dialog body mounts fields asynchronously; wait until it overflows so
	// the wheel scrolls instead of racing the content height.
	await expect
		.poll(() =>
			dialog.evaluate((element) => element.scrollHeight > element.clientHeight + 1),
		)
		.toBe(true);
	await dialog.hover();
	await page.mouse.wheel(0, 3000);
	await expect(save).toBeInViewport();
});

test("admin approval set editor reaches its save action on a short screen", async ({
	page,
}) => {
	await page.setViewportSize({ width: 1280, height: 320 });
	await page.goto("/admin/approval-sets/new");
	await expect(page.getByTestId("approval-set-name")).toBeVisible();
	const scroll = page.getByTestId("approval-set-detail-scroll");
	await expect
		.poll(() =>
			scroll.evaluate((element) => element.scrollHeight > element.clientHeight + 1),
		)
		.toBe(true);
	const save = page.getByTestId("approval-set-save");
	await expect(save).toBeAttached();
	await expect(save).not.toBeInViewport();
	await scroll.hover();
	await page.mouse.wheel(0, 3000);
	await expect(save).toBeInViewport();
	await expectInsideViewport(scroll);
});

test("notification tray reaches View all on a short screen", async ({
	browser,
	request,
}) => {
	const userData = generateUser("scroll-notifications");
	const user = await createUserViaAPI(request, userData);
	const context = await browser.newContext({
		baseURL: process.env.BASE_URL || "http://localhost:8080",
		storageState: { cookies: [], origins: [] },
		viewport: { width: 1280, height: 480 },
	});
	try {
		const login = await context.request.post("/api/auth/login", {
			headers: { "Sec-Fetch-Site": "same-origin" },
			data: {
				email_or_username: userData.username,
				password: userData.password_hash,
			},
		});
		expect(login.ok(), await login.text()).toBeTruthy();
		for (let index = 0; index < 12; index++) {
			const response = await context.request.post("/api/notifications", {
				headers: { "Sec-Fetch-Site": "same-origin" },
				data: {
					type: "info",
					title: `Scroll notification ${index}`,
					message: "Scroll test message",
					action_url: "/notifications",
				},
			});
			expect(response.ok(), await response.text()).toBeTruthy();
		}
		const page = await context.newPage();
		await page.goto("/");
		await page.getByTestId("notifications-trigger").click();
		const list = page.getByTestId("notifications-scroll");
		await expect(list).toContainText("Scroll notification 0");
		await list.hover();
		await page.mouse.wheel(0, 3000);
		const viewAll = page.getByTestId("notifications-view-all");
		await expect(viewAll).toBeInViewport();
		await expectInsideViewport(page.getByTestId("notifications-menu"));
		await viewAll.click();
		await expect(page).toHaveURL(/\/notifications$/);
	} finally {
		await context.close();
		const response = await request.delete(`/api/users/${user.id}`, {
			headers: { "Sec-Fetch-Site": "same-origin" },
		});
		expect(response.ok(), await response.text()).toBeTruthy();
	}
});

test("icon picker reaches its last color on a short screen", async ({
	page,
	request,
}) => {
	const workspace = await createWorkspaceViaAPI(
		request,
		generateWorkspace("scroll-icon"),
	);
	try {
		const response = await request.post(
			`/api/v2/workspaces/${workspace.id}/pages`,
			{
				data: { title: "Icon scrolling", content: "" },
			},
		);
		expect(response.ok(), await response.text()).toBeTruthy();
		const created = (await response.json()).data;
		await page.setViewportSize({ width: 1280, height: 320 });
		await page.goto(`/workspaces/${workspace.id}/pages/${created.id}`);
		// Wait for the page data and title row to settle so the picker anchors to
		// the trigger's final position, not the pre-layout one.
		await expect(page.locator("#page-title-input")).toHaveValue("Icon scrolling");
		const trigger = page.getByTestId("page-icon-picker-trigger");
		await waitForStableBox(trigger);
		await trigger.click();
		const menu = page.getByTestId("page-icon-picker-menu");
		await waitForStableBox(menu);
		const last = page.getByTestId("page-icon-picker-color").last();
		await scrollToContent(page, menu, last);
		await expect(last).toBeInViewport();
		await expectInsideViewport(menu);
		await last.click();
		await page.keyboard.press("Escape");
	} finally {
		expect(
			(await request.delete(`/api/v2/workspaces/${workspace.id}`)).status(),
		).toBe(204);
	}
});

test("page work-item search reaches its last result on a short screen", async ({
	page,
	request,
}) => {
	const workspace = await createWorkspaceViaAPI(
		request,
		generateWorkspace("scroll-page-links"),
	);
	try {
		for (let index = 0; index < 10; index++) {
			await createItemViaAPI(request, workspace.id, {
				title: `${workspace.key} scroll result ${index}`,
			});
		}
		const response = await request.post(
			`/api/v2/workspaces/${workspace.id}/pages`,
			{
				data: { title: "Work item search scrolling", content: "" },
			},
		);
		expect(response.ok(), await response.text()).toBeTruthy();
		const created = (await response.json()).data;
		await page.setViewportSize({ width: 1280, height: 480 });
		await page.goto(`/workspaces/${workspace.id}/pages/${created.id}`);
		await page.getByTestId("page-work-items-trigger").click();
		await page.getByTestId("page-work-items-add").click();
		await page
			.getByTestId("page-work-items-add-search")
			.fill(`${workspace.key} scroll result`);
		const results = page.getByTestId("page-work-items-add-result");
		await expect(results).toHaveCount(10);
		await page.getByTestId("page-work-items-search-list").hover();
		await page.mouse.wheel(0, 3000);
		const last = results.last();
		const title = (await last.innerText()).split("\n")[0];
		await expect(last).toBeInViewport();
		await expectInsideViewport(page.getByTestId("page-work-items-popover"));
		await last.click();
		await expect(page.getByTestId("page-work-items-row")).toContainText(title);
		await page.reload();
		await expect(page.getByTestId("page-work-items-count")).toHaveText("1");
	} finally {
		expect(
			(await request.delete(`/api/v2/workspaces/${workspace.id}`)).status(),
		).toBe(204);
	}
});
