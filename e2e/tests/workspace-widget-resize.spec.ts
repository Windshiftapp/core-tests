import { test, expect } from '../fixtures/errors';
import { authenticateAdminRequest, createWorkspaceViaAPI } from '../fixtures/api-helpers';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

// Widget resize controls belong to customize mode: the grip and the width menu
// must stay out of the resting dashboard and only appear once the user starts
// customizing.
test.describe('workspace overview widget resizing', () => {
	test('reveals the resize controls only in customize mode and persists a dragged width', async ({
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

		// Resting state: no resize affordances.
		await expect(widget.getByTestId('widget-resize-handle')).toHaveCount(0);
		await expect(widget.getByTestId('widget-width-menu')).toHaveCount(0);

		// Entering customize mode reveals the handle and the width menu.
		await page.getByTestId('workspace-dashboard-customize').click();

		const handle = widget.getByTestId('widget-resize-handle');
		await expect(handle).toBeVisible();
		await expect(widget.getByTestId('widget-width-menu')).toBeVisible();
		await expect(widget.getByTestId('widget-resize-grip')).toBeVisible();

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

		// The width survives a reload and the controls are hidden again.
		await page.reload();
		const reloaded = page.getByTestId('widget-w-stats');
		await expect(reloaded).toHaveAttribute('data-widget-width', '3', { timeout: 15_000 });
		await expect(reloaded.getByTestId('widget-resize-handle')).toHaveCount(0);
		await expect(reloaded.getByTestId('widget-width-menu')).toHaveCount(0);
	});
});
