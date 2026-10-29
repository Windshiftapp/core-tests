import {
	authenticateAdminRequest,
	createItemViaAPI,
	createWorkspaceViaAPI,
} from "../fixtures/api-helpers";
import { expect, test } from "../fixtures/context-path";
import { generateItem, generateWorkspace } from "../fixtures/test-data";

/**
 * Board chrome contract.
 *
 * The board toolbar (view header + filters) is fixed to the viewport width:
 * it never scrolls horizontally and never leaves the viewport, so its
 * right-hand controls are reachable without panning. Only the lane canvas
 * scrolls, panning underneath the toolbar in both axes.
 */

test.describe.configure({ retries: 0 });

test.describe("Board chrome", () => {
	test("toolbar stays put while columns pan underneath it", async ({
		page,
		request,
	}) => {
		test.setTimeout(60_000);
		await page.setViewportSize({ width: 800, height: 600 });
		await authenticateAdminRequest(request);

		const suffix = `bch${Date.now()}`;
		const ws = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
		for (let index = 0; index < 12; index++) {
			const itemData = generateItem(ws.id, `${suffix}-${index}`);
			await createItemViaAPI(request, ws.id, { title: itemData.title });
		}

		try {
			await page.goto(`/workspaces/${ws.id}/board`);
			await expect(page.getByTestId("board-view")).toBeVisible({
				timeout: 15_000,
			});
			const container = page.getByTestId("board-scroll-container");
			const header = page.getByTestId("board-header");
			const viewportWidth = await page.evaluate(() => window.innerWidth);

			// The toolbar is viewport-width, not board-width.
			const headerBox = await header.boundingBox();
			expect(headerBox).not.toBeNull();
			expect(headerBox!.width).toBeLessThanOrEqual(viewportWidth);
			expect(headerBox!.x + headerBox!.width).toBeLessThanOrEqual(
				viewportWidth + 1,
			);

			// The lane canvas is the only scroller and it overflows horizontally.
			const overflow = await container.evaluate((el) => ({
				scrollWidth: el.scrollWidth,
				clientWidth: el.clientWidth,
			}));
			expect(overflow.scrollWidth).toBeGreaterThan(overflow.clientWidth);

			// Pan the lanes right: the toolbar must not move, the columns must.
			const headerXBefore = (await header.boundingBox())!.x;
			const rightmostBefore = (await page
				.getByTestId("board-column")
				.last()
				.boundingBox())!.x;
			await container.hover();
			await page.mouse.wheel(600, 0);
			await expect
				.poll(() => container.evaluate((el) => el.scrollLeft))
				.toBeGreaterThan(0);
			const headerXAfter = (await header.boundingBox())!.x;
			const rightmostAfter = (await page
				.getByTestId("board-column")
				.last()
				.boundingBox())!.x;

			expect(headerXAfter).toBe(headerXBefore);
			expect(rightmostAfter).toBeLessThan(rightmostBefore);
			// The rightmost column is now reachable while the toolbar stayed put.
			await expect(page.getByTestId("board-column").last()).toBeInViewport();
			await expect(page.getByTestId("board-view-switcher")).toBeInViewport();
		} finally {
			expect(
				(await request.delete(`/api/v2/workspaces/${ws.id}`)).status(),
			).toBe(204);
		}
	});

	test("toolbar stays pinned while the lanes scroll vertically", async ({
		page,
		request,
	}) => {
		test.setTimeout(60_000);
		await page.setViewportSize({ width: 800, height: 600 });
		await authenticateAdminRequest(request);

		const suffix = `bcv${Date.now()}`;
		const ws = await createWorkspaceViaAPI(request, generateWorkspace(suffix));
		for (let index = 0; index < 12; index++) {
			const itemData = generateItem(ws.id, `${suffix}-${index}`);
			await createItemViaAPI(request, ws.id, { title: itemData.title });
		}

		try {
			await page.goto(`/workspaces/${ws.id}/board`);
			await expect(page.getByTestId("board-view")).toBeVisible({
				timeout: 15_000,
			});
			const container = page.getByTestId("board-scroll-container");
			const header = page.getByTestId("board-header");
			const headerBoxBefore = (await header.boundingBox())!;

			await container.hover();
			await page.mouse.wheel(0, 900);
			await expect
				.poll(() => container.evaluate((el) => el.scrollTop))
				.toBeGreaterThan(0);

			const headerBoxAfter = (await header.boundingBox())!;
			expect(headerBoxAfter.y).toBe(headerBoxBefore.y);
			await expect(header).toBeInViewport();
			// The toolbar lives outside the scroller, not pinned inside it.
			const insideScroller = await header.evaluate(
				(el) =>
					el.closest('[data-testid="board-scroll-container"]') !== null,
			);
			expect(insideScroller).toBe(false);
		} finally {
			expect(
				(await request.delete(`/api/v2/workspaces/${ws.id}`)).status(),
			).toBe(204);
		}
	});

	test("narrow viewport keeps the toolbar inside the viewport", async ({
		page,
		request,
	}) => {
		test.setTimeout(60_000);
		await page.setViewportSize({ width: 800, height: 600 });
		await authenticateAdminRequest(request);

		const suffix = `bcn${Date.now()}`;
		const ws = await createWorkspaceViaAPI(request, generateWorkspace(suffix));

		try {
			await page.goto(`/workspaces/${ws.id}/board`);
			await expect(page.getByTestId("board-view")).toBeVisible({
				timeout: 15_000,
			});

			const viewportWidth = await page.evaluate(() => window.innerWidth);
			const header = page.getByTestId("board-header");
			const headerBox = (await header.boundingBox())!;

			// No horizontal page overflow and no page-level scrolling: the
			// toolbar adapts to the viewport instead of forcing overflow.
			const doc = await page.evaluate(() => ({
				scrollWidth: document.documentElement.scrollWidth,
				clientWidth: document.documentElement.clientWidth,
			}));
			expect(doc.scrollWidth).toBe(doc.clientWidth);
			expect(headerBox.width).toBeLessThanOrEqual(viewportWidth);
			await expect(page.getByTestId("board-view-switcher")).toBeInViewport();
		} finally {
			expect(
				(await request.delete(`/api/v2/workspaces/${ws.id}`)).status(),
			).toBe(204);
		}
	});

	test("chrome matches the header layout of the other collection views", async ({
		page,
		request,
	}) => {
		test.setTimeout(60_000);
		// The board header carries grouping, sorting, and the selection toggle
		// next to the view switcher, so it needs a wide viewport to keep all
		// controls on one line. Below this the header wraps, which is the
		// intended responsive behavior, not a layout drift.
		await page.setViewportSize({ width: 1600, height: 800 });
		await authenticateAdminRequest(request);

		const suffix = `bcl${Date.now()}`;
		const ws = await createWorkspaceViaAPI(request, generateWorkspace(suffix));

		const subtitleTop = async () => {
			await page.evaluate(() => document.fonts.ready);
			const box = await page
				.getByTestId("page-header-subtitle")
				.boundingBox();
			expect(box).not.toBeNull();
			return Math.round(box!.y);
		};

		try {
			await page.goto(`/workspaces/${ws.id}/board`);
			await expect(page.getByTestId("board-view")).toBeVisible({
				timeout: 15_000,
			});
			const boardTop = await subtitleTop();

			// The board header starts at the same offset as the other views'.
			for (const [view, testid] of [
				["list", "list-view"],
				["tree", "tree-view"],
				["backlog", "backlog-view"],
			] as const) {
				await page.goto(`/workspaces/${ws.id}/${view}`);
				await expect(page.getByTestId(testid)).toBeVisible({
					timeout: 15_000,
				});
				expect(await subtitleTop()).toBe(boardTop);
			}

			// The board chrome separates itself with whitespace, not rules.
			await page.goto(`/workspaces/${ws.id}/board`);
			await expect(page.getByTestId("board-view")).toBeVisible({
				timeout: 15_000,
			});
			for (const testid of ["board-header", "board-controls"]) {
				const border = await page
					.getByTestId(testid)
					.evaluate((el) => getComputedStyle(el).borderBottomWidth);
				expect(border).toBe("0px");
			}
		} finally {
			expect(
				(await request.delete(`/api/v2/workspaces/${ws.id}`)).status(),
			).toBe(204);
		}
	});
});
