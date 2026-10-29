import { expect, type Locator, type Page } from "@playwright/test";

// Subpixel slop for containment checks: rounding can leave the target a
// fraction of a pixel outside the visible rect with the container already at
// its maximum scroll offset.
const CONTAINMENT_TOLERANCE_PX = 1;

/**
 * Assert the target is fully inside the surface's visible rect, allowing the
 * same 1px of scroll rounding as scrollToContent. Use this instead of
 * toBeInViewport(ratio 1), which has no subpixel tolerance.
 */
export async function expectContained(surface: Locator, target: Locator) {
	const visible = await visibleRect(surface);
	const bounds = await target.boundingBox();
	if (!bounds) throw new Error("Scroll target is not rendered");
	const visibleHeight =
		Math.min(bounds.y + bounds.height, visible.bottom) -
		Math.max(bounds.y, visible.top);
	expect(
		visibleHeight,
		"scroll target should be fully inside the visible rect",
	).toBeGreaterThanOrEqual(bounds.height - CONTAINMENT_TOLERANCE_PX);
}

async function visibleRect(surface: Locator) {
	return surface.evaluate((element) => {
		const bounds = element.getBoundingClientRect();
		let left = Math.max(0, bounds.left);
		let right = Math.min(window.innerWidth, bounds.right);
		let top = Math.max(0, bounds.top);
		let bottom = Math.min(window.innerHeight, bounds.bottom);
		for (let node = element.parentElement; node; node = node.parentElement) {
			if (
				["auto", "scroll", "hidden"].includes(getComputedStyle(node).overflowY)
			) {
				const rect = node.getBoundingClientRect();
				left = Math.max(left, rect.left);
				right = Math.min(right, rect.right);
				top = Math.max(top, rect.top);
				bottom = Math.min(bottom, rect.bottom);
			}
		}
		return { left, right, top, bottom };
	});
}

// Use viewport-sized gestures: Firefox caps oversized wheel events.
export async function scrollToContent(
	page: Page,
	surface: Locator,
	target: Locator,
) {
	for (let gesture = 0; gesture < 200; gesture++) {
		const visible = await visibleRect(surface);
		const bounds = await target.boundingBox();
		if (!bounds) throw new Error("Scroll target is not rendered");
		if (
			bounds.y >= visible.top - CONTAINMENT_TOLERANCE_PX &&
			bounds.y + bounds.height <= visible.bottom + CONTAINMENT_TOLERANCE_PX
		) {
			// Assert visibility numerically instead of toBeInViewport(ratio 1):
			// ratio has no subpixel tolerance, which deadlocks on rounding.
			const visibleHeight =
				Math.min(bounds.y + bounds.height, visible.bottom) -
				Math.max(bounds.y, visible.top);
			expect(
				visibleHeight,
				"scroll target should be fully inside the visible rect",
			).toBeGreaterThanOrEqual(bounds.height - CONTAINMENT_TOLERANCE_PX);
			return;
		}
		const height = visible.bottom - visible.top;
		if (height <= 0 || visible.right <= visible.left)
			throw new Error("Scroll surface is not visible");
		const direction = bounds.y < visible.top ? -1 : 1;
		await page.mouse.move(
			(visible.left + visible.right) / 2,
			(visible.top + visible.bottom) / 2,
		);
		const remaining =
			direction < 0
				? visible.top - bounds.y
				: bounds.y + bounds.height - visible.bottom;
		await page.mouse.wheel(
			0,
			direction * Math.min(height * 0.8, remaining + 4),
		);
		await expect
			.poll(
				async () => {
					const next = await target.boundingBox();
					if (!next) throw new Error("Scroll target disappeared");
					const moved = direction * (bounds.y - next.y);
					if (moved > 0) return moved;
					// The container may already sit at its maximum scroll offset
					// (the gesture only overshot by a few pixels). Treat the target
					// as done once it is contained, even if the wheel moved nothing.
					const nextVisible = await visibleRect(surface);
					const contained =
						next.y >= nextVisible.top - CONTAINMENT_TOLERANCE_PX &&
						next.y + next.height <=
							nextVisible.bottom + CONTAINMENT_TOLERANCE_PX;
					return contained ? moved + 1 : moved;
				},
				// A wheel scroll settles within a frame. Keep this short so a
				// gesture aimed at a since-repositioned surface retries with a
				// fresh read instead of blocking the whole run on one timeout.
				{ timeout: 2_000 },
			)
			.toBeGreaterThan(0);
	}
	throw new Error("Scroll target was not reachable within 200 gestures");
}
