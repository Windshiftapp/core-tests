import { test, expect } from '../fixtures/errors';
import { authenticateAdminRequest, createWorkspaceViaAPI } from '../fixtures/api-helpers';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

// The workspace overview widget resize handle used to be a transparent 6px
// strip that only rendered in customize mode. It is now always present, shows a
// grip on widget hover, and drags to a persisted column width.
test.describe('workspace overview widget resizing', () => {
	test('reveals the resize grip on hover and persists a dragged width', async ({
		page,
		request,
	}) => {
		const stamp = Date.now();
		await authenticateAdminRequest(request);
		const ws = await createWorkspaceViaAPI(request, {
			name: `widget-resize-${stamp}`,
			key: `WR${stamp.toString().slice(-7)}`.toUpperCase(),
			description: 'widget resize e2e',
		});

		const layout = await request.put(
			`${BASE_URL}/api/workspaces/${ws.id}/homepage/layout`,
			{
				headers: SEC_FETCH,
				data: {
					sections: [
						{
							id: 'resize-section',
							title: 'Resize',
							display_order: 0,
							widget_ids: ['w-stats', 'w-created'],
						},
					],
					widgets: [
						{
							id: 'w-stats',
							type: 'stats',
							section_id: 'resize-section',
							position: 0,
							width: 1,
							config: {},
						},
						{
							id: 'w-created',
							type: 'created-chart',
							section_id: 'resize-section',
							position: 1,
							width: 2,
							config: {},
						},
					],
				},
			},
		);
		expect(layout.ok(), `layout save: ${layout.status()} ${await layout.text()}`).toBeTruthy();

		await page.goto(`/workspaces/${ws.id}/overview`);

		const widget = page.getByTestId('widget-w-stats');
		await expect(widget).toBeVisible({ timeout: 15_000 });
		await expect(widget).toHaveAttribute('data-widget-width', '1');

		const handle = widget.getByTestId('widget-resize-handle');
		const grip = widget.getByTestId('widget-resize-grip');

		// Resting state: the handle is available but out of the tab order and
		// the grip stays hidden until the widget is hovered.
		await expect(handle).toHaveAttribute('tabindex', '-1');
		await expect(grip).toHaveCSS('opacity', '0');

		await widget.hover();
		await expect(grip).toHaveCSS('opacity', '1');

		// Drag past the widget's max width; it must clamp to the registry max.
		const box = await handle.boundingBox();
		expect(box, 'resize handle has a box').not.toBeNull();
		await page.mouse.move(box!.x + box!.width / 2, box!.y + box!.height / 2);
		await page.mouse.down();
		await page.mouse.move(box!.x + 800, box!.y + box!.height / 2, { steps: 8 });

		// The layout save is debounced; wait for it before reloading.
		const saved = page.waitForResponse(
			(response) =>
				response.request().method() === 'PUT' &&
				response.url().includes(`/api/workspaces/${ws.id}/homepage/layout`),
		);
		await page.mouse.up();
		await saved;

		await expect(widget).toHaveAttribute('data-widget-width', '3');

		// The width survives a reload.
		await page.reload();
		await expect(page.getByTestId('widget-w-stats')).toHaveAttribute(
			'data-widget-width',
			'3',
			{ timeout: 15_000 },
		);
	});
});
