import { createWorkspaceViaAPI } from "../fixtures/api-helpers";
import { expect, test } from "../fixtures/context-path";
import { generateWorkspace } from "../fixtures/test-data";
import { expectContained, scrollToContent } from "../helpers/scroll-to-content";
import { KnowledgePage } from "../pages/knowledge.page";

test.afterEach(async ({ page }, testInfo) => {
	if (
		testInfo.status === testInfo.expectedStatus ||
		!testInfo.title.startsWith("long page")
	)
		return;
	const layout = await page
		.getByTestId("page-editor")
		.getByText("Final paragraph.", { exact: true })
		.evaluate((el) => {
			const chain = [];
			for (let node = el; node; node = node.parentElement) {
				const rect = node.getBoundingClientRect();
				const css = getComputedStyle(node);
				chain.push({
					tag: node.tagName,
					className: node.className,
					top: rect.top,
					bottom: rect.bottom,
					height: rect.height,
					scrollHeight: node.scrollHeight,
					clientHeight: node.clientHeight,
					scrollTop: node.scrollTop,
					overflowY: css.overflowY,
					flex: css.flex,
				});
			}
			return { chain, fonts: document.fonts.status };
		});
	await testInfo.attach("layout.json", {
		body: JSON.stringify(layout, null, 2),
		contentType: "application/json",
	});
});

for (const mode of ["edit", "read"]) {
	test(`long page can scroll to its end in ${mode} mode @critical-browser`, async ({
		page,
		request,
	}, testInfo) => {
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace("page-content-end"),
		);
		try {
			const response = await request.post(
				`/api/v2/workspaces/${workspace.id}/pages`,
				{
					data: {
						title: "Long document",
						content: `${"# Section\n\nA paragraph of content.\n\n".repeat(80)}Final paragraph.`,
					},
				},
			);
			expect(response.ok()).toBe(true);
			const created = (await response.json()).data;
			await page.setViewportSize({ width: 1280, height: 480 });
			await page.goto(`/workspaces/${workspace.id}/pages/${created.id}`);
			await page.getByTestId(`page-mode-${mode}`).click();
			await expect(page.getByTestId(`page-mode-${mode}`)).toHaveAttribute(
				"aria-pressed",
				"true",
			);
			const final = page
				.getByTestId("page-editor")
				.getByText("Final paragraph.", { exact: true });
			await expect(final).toBeAttached();
			await page.evaluate(() => document.fonts.ready);
			// The document must stay within its canvas, including the last paragraph.
			await expect
				.poll(async () => {
					const canvas = await page.getByTestId("page-canvas").boundingBox();
					const paragraph = await final.boundingBox();
					if (!canvas || !paragraph)
						throw new Error("Document is not rendered");
					return canvas.y + canvas.height - (paragraph.y + paragraph.height);
				})
				.toBeGreaterThanOrEqual(0);
			await scrollToContent(page, page.getByTestId("pages-view"), final);
			await expectContained(page.getByTestId("pages-view"), final);
			await expect(page.locator("#pages-add-button")).toBeInViewport();
			await page.setViewportSize({ width: 900, height: 320 });
			await scrollToContent(page, page.getByTestId("pages-view"), final);
			await expectContained(page.getByTestId("pages-view"), final);
			await expect(page.locator("#pages-add-button")).toBeInViewport();
			await page.screenshot({
				path: testInfo.outputPath(`page-content-${mode}.png`),
			});
		} finally {
			expect(
				(await request.delete(`/api/v2/workspaces/${workspace.id}`)).status(),
			).toBe(204);
		}
	});
}

test("header action row meets the editor toolbar divider on the right", async ({
	page,
	request,
}) => {
	const workspace = await createWorkspaceViaAPI(
		request,
		generateWorkspace("page-toolbar-align"),
	);
	try {
		const response = await request.post(
			`/api/v2/workspaces/${workspace.id}/pages`,
			{ data: { title: "Alignment", content: "Body." } },
		);
		expect(response.ok(), await response.text()).toBeTruthy();
		const created = (await response.json()).data;

		// 1440px is above the 1281px breakpoint where the page frame shrinks
		// to 75% of the pane; 1100px keeps the frame at full pane width. The
		// header controls must share the divider's right edge in both frames.
		for (const width of [1440, 1100]) {
			await page.setViewportSize({ width, height: 900 });
			await page.goto(`/workspaces/${workspace.id}/pages/${created.id}`);
			const kebab = page.getByTestId("page-toolbar-kebab");
			const divider = page
				.getByTestId("page-editor")
				.getByTestId("milkdown-toolbar");
			await expect(kebab).toBeVisible();
			await expect(divider).toBeVisible();
			await page.evaluate(() => document.fonts.ready);
			await expect
				.poll(async () => {
					const kebabBox = await kebab.boundingBox();
					const dividerBox = await divider.boundingBox();
					if (!kebabBox || !dividerBox)
						throw new Error("Toolbars are not rendered");
					return Math.abs(
						kebabBox.x + kebabBox.width - (dividerBox.x + dividerBox.width),
					);
				})
				.toBeLessThanOrEqual(1);
		}
	} finally {
		expect(
			(await request.delete(`/api/v2/workspaces/${workspace.id}`)).status(),
		).toBe(204);
	}
});

test("a long title wraps instead of clipping in edit and read mode", async ({
	page,
	request,
}) => {
	const workspace = await createWorkspaceViaAPI(
		request,
		generateWorkspace("page-title-wrap"),
	);
	try {
		const longTitle =
			"Bulk Work Item Operations — API and Atomic Updates for Concurrent Editors";
		const shortTitle = "Short title";
		const created: Array<{ id: number; title: string }> = [];
		for (const title of [longTitle, shortTitle]) {
			const response = await request.post(
				`/api/v2/workspaces/${workspace.id}/pages`,
				{ data: { title, content: "Body." } },
			);
			expect(response.ok(), await response.text()).toBeTruthy();
			created.push((await response.json()).data);
		}
		const [longPage, shortPage] = created;

		// 1440px is above the 1281px breakpoint where the page frame shrinks to
		// 75% of the pane — the narrow frame that used to clip a long title.
		await page.setViewportSize({ width: 1440, height: 900 });
		await page.goto(`/workspaces/${workspace.id}/pages/${longPage.id}`);
		const knowledge = new KnowledgePage(page);
		await expect(knowledge.titleInput).toHaveValue(longTitle);

		const titleMetrics = () =>
			knowledge.titleInput.evaluate((el) => {
				const lineHeight = Number.parseFloat(getComputedStyle(el).lineHeight);
				return {
					clippedHorizontally: el.scrollWidth > el.clientWidth + 1,
					clippedVertically: el.scrollHeight > el.clientHeight + 1,
					wrapped: el.clientHeight > lineHeight * 1.5,
				};
			});

		for (const mode of ["edit", "read"] as const) {
			await page.getByTestId(`page-mode-${mode}`).click();
			await expect(page.getByTestId(`page-mode-${mode}`)).toHaveAttribute(
				"aria-pressed",
				"true",
			);
			await expect.poll(titleMetrics).toEqual({
				clippedHorizontally: false,
				clippedVertically: false,
				wrapped: true,
			});
		}

		// Switching pages in place must re-measure the height for the new title.
		await knowledge.selectPage(shortPage.id);
		await expect(knowledge.titleInput).toHaveValue(shortTitle);
		await expect.poll(titleMetrics).toEqual({
			clippedHorizontally: false,
			clippedVertically: false,
			wrapped: false,
		});
		await knowledge.selectPage(longPage.id);
		await expect(knowledge.titleInput).toHaveValue(longTitle);
		await expect.poll(titleMetrics).toEqual({
			clippedHorizontally: false,
			clippedVertically: false,
			wrapped: true,
		});
	} finally {
		expect(
			(await request.delete(`/api/v2/workspaces/${workspace.id}`)).status(),
		).toBe(204);
	}
});
