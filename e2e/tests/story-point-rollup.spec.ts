import { randomUUID } from 'node:crypto';
import { type APIRequestContext } from '../fixtures/context-path';
import { createItemViaAPI, createWorkspaceViaAPI } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';

/**
 * Story point rollup display (GH #256 / WI-1341).
 *
 * Contract: an item with children holding story points shows the computed
 * child rollup on the story-points sidebar row. Display-only — the parent's
 * own points stay manually editable.
 */

/**
 * Story points is not on the default screen; add it to the screen layout the
 * item's type actually renders with (resolved from the item's own
 * detail-summary screen context; workspaces without explicit bindings fall
 * back to screen 1 via the canonical effective-config resolution).
 */
async function enableStoryPointsOnScreen(
	request: APIRequestContext,
	itemId: number
): Promise<void> {
	const summary = await (
		await request.get(`/api/v2/items/${itemId}/detail-summary`)
	).json();
	const screenId = summary.data?.screen_context?.view?.id ?? summary.data?.screen_context?.edit?.id ?? 1;
	const screen = await (await request.get(`/api/v2/screens/${screenId}`)).json();
	const fields = (screen.data?.fields ?? []).map((field) => ({
		field_type: field.field_type,
		field_identifier: field.field_identifier,
		display_order: field.display_order,
		is_required: field.is_required,
		field_width: field.field_width,
	}));
	fields.push({
		field_type: 'system',
		field_identifier: 'story_points',
		display_order: fields.length,
		is_required: false,
		field_width: 'half',
	});
	const put = await request.put(`/api/v2/screens/${screenId}/fields`, {
		headers: { 'Content-Type': 'application/json' },
		data: { fields },
	});
	expect(
		put.ok(),
		`enable story points on screen ${screenId} failed (${put.status()}): ${await put.text()}`
	).toBeTruthy();
}

test('shows the child story point rollup on the parent item detail', async ({
	request,
	page,
}) => {
	const workspace = await createWorkspaceViaAPI(request, {
		name: `SP Rollup ${Date.now()}`,
		key: `SPR${randomUUID().replaceAll('-', '').slice(0, 7).toUpperCase()}`,
		description: 'Story point rollup display E2E',
	});

	const parent = await createItemViaAPI(request, workspace.id, {
		title: 'Parent story',
	});
	const childA = await createItemViaAPI(request, workspace.id, {
		title: 'Child task A',
		parent_id: parent.id,
		story_points: 3,
	});
	await createItemViaAPI(request, workspace.id, {
		title: 'Child task B',
		parent_id: parent.id,
		story_points: 5,
	});
	// Grandchild under child A — the rollup is recursive across levels.
	await createItemViaAPI(request, workspace.id, {
		title: 'Grandchild',
		parent_id: childA.id,
		story_points: 2,
	});
	await enableStoryPointsOnScreen(request, parent.id);

	const summary = await (await request.get(`/api/v2/items/${parent.id}/detail-summary`)).json();
	console.log('SUMMARY_CHILDREN:', JSON.stringify(summary.data?.children));

	await page.goto(`/workspaces/${workspace.key}/items/${parent.workspace_item_number}`);

	// Guard against surprise navigation: the detail must still be the parent.
	await expect(page).toHaveURL(new RegExp(`/items/${parent.id}(\\?|$)`));

	const rollup = page.getByTestId('story-points-child-rollup');
	await expect(rollup).toHaveText(/10 pts rolled up from 3 child items/);
});

test('does not show the rollup hint without children holding points', async ({
	request,
	page,
}) => {
	const workspace = await createWorkspaceViaAPI(request, {
		name: `SP NoRollup ${Date.now()}`,
		key: `SPN${randomUUID().replaceAll('-', '').slice(0, 7).toUpperCase()}`,
		description: 'Story point rollup negative E2E',
	});
	const parent = await createItemViaAPI(request, workspace.id, {
		title: 'Parent without points',
	});
	await createItemViaAPI(request, workspace.id, {
		title: 'Child without points',
		parent_id: parent.id,
	});
	await enableStoryPointsOnScreen(request, parent.id);

	await page.goto(`/workspaces/${workspace.key}/items/${parent.workspace_item_number}`);

	await expect(page.getByTestId('story-points-child-rollup')).toHaveCount(0);
});
