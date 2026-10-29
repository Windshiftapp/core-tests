import { randomUUID } from 'node:crypto';
import { type APIRequestContext } from '../fixtures/context-path';
import {
	createItemViaAPI,
	createWorkspaceViaAPI,
	listWorkspaceItemTypesViaAPI,
} from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';

/**
 * Backlog field availability (WI-1747).
 *
 * Contract: the backlog shows the story-points control only when the item's
 * effective edit screen configures story_points. Resolution goes through the
 * shared effective-config cache, so the control tracks the same configuration
 * the item detail uses and updates after a screen change.
 *
 * Each test binds its own screen and config set to the workspace so the
 * shared default screen is never mutated.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

function workspaceKey(): string {
	return `BSP${randomUUID().replaceAll('-', '').slice(0, 7).toUpperCase()}`;
}

interface BacklogFixture {
	workspace: { id: number };
	item: { id: number; item_type_id: number; title: string };
	configSetId: number;
}

async function createBacklogFixture(
	request: APIRequestContext,
	withStoryPoints: boolean,
): Promise<BacklogFixture> {
	const suffix = workspaceKey();
	const workspace = await createWorkspaceViaAPI(request, {
		name: `Backlog SP ${suffix}`,
		key: suffix,
		description: 'Backlog story-points availability E2E',
	});
	const itemTypes = await listWorkspaceItemTypesViaAPI(request, workspace.id);
	const itemTypeId: number | undefined = itemTypes[0]?.id;
	expect(itemTypeId, 'workspace has no item type').toBeGreaterThan(0);

	const screenResponse = await request.post('/api/screens', {
		headers: SEC_FETCH,
		data: { name: `Backlog SP screen ${suffix}`, description: 'e2e' },
	});
	expect(
		screenResponse.ok(),
		`create screen failed (${screenResponse.status()}): ${await screenResponse.text()}`,
	).toBeTruthy();
	const screen = await screenResponse.json();

	const fields = [
		{ field_type: 'system', field_identifier: 'title', display_order: 0, is_required: true, field_width: 'full' },
		{ field_type: 'system', field_identifier: 'description', display_order: 1, is_required: false, field_width: 'full' },
		{ field_type: 'system', field_identifier: 'status', display_order: 2, is_required: true, field_width: 'half' },
	];
	if (withStoryPoints) {
		fields.push({
			field_type: 'system',
			field_identifier: 'story_points',
			display_order: fields.length,
			is_required: false,
			field_width: 'half',
		});
	}
	const fieldsResponse = await request.put(`/api/screens/${screen.id}/fields`, {
		headers: SEC_FETCH,
		data: fields,
	});
	expect(
		fieldsResponse.ok(),
		`set screen fields failed (${fieldsResponse.status()}): ${await fieldsResponse.text()}`,
	).toBeTruthy();

	const configResponse = await request.post('/api/configuration-sets', {
		headers: SEC_FETCH,
		data: {
			name: `Backlog SP config ${suffix}`,
			description: 'e2e',
			workspace_ids: [workspace.id],
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
	expect(
		configResponse.ok(),
		`create config set failed (${configResponse.status()}): ${await configResponse.text()}`,
	).toBeTruthy();
	const configSet = await configResponse.json();

	const item = await createItemViaAPI(request, workspace.id, {
		title: `Backlog SP item ${suffix}`,
		item_type_id: itemTypeId,
	});

	return { workspace, item, configSetId: configSet.id };
}

async function cleanup(request: APIRequestContext, fixture: BacklogFixture): Promise<void> {
	await request.delete(`/api/configuration-sets/${fixture.configSetId}`, {
		headers: SEC_FETCH,
	});
}

test('hides the backlog story-points control when the effective screen omits it', async ({
	request,
	page,
}) => {
	const fixture = await createBacklogFixture(request, false);
	try {
		await page.goto(`/workspaces/${fixture.workspace.id}/backlog`);
		await expect(page.getByTestId('backlog-item')).toContainText(fixture.item.title, {
			timeout: 20_000,
		});
		await expect(page.getByTestId(`backlog-story-points-${fixture.item.id}`)).toHaveCount(0);
	} finally {
		await cleanup(request, fixture);
	}
});

test('shows and persists the backlog story-points control when the effective screen has it', async ({
	request,
	page,
}) => {
	const fixture = await createBacklogFixture(request, true);
	try {
		await page.goto(`/workspaces/${fixture.workspace.id}/backlog`);
		await expect(page.getByTestId('backlog-item')).toContainText(fixture.item.title, {
			timeout: 20_000,
		});

		await expect(page.getByTestId(`backlog-story-points-${fixture.item.id}`)).toBeVisible({
			timeout: 20_000,
		});

		await page.getByTestId(`backlog-story-points-button-${fixture.item.id}`).click();
		const input = page.getByTestId(`backlog-story-points-input-${fixture.item.id}`);
		await input.fill('5');
		await input.press('Enter');

		await expect(page.getByTestId(`backlog-story-points-button-${fixture.item.id}`)).toContainText(
			'5',
		);

		// Revisit to prove the value persisted through the API, not just the store.
		await page.reload();
		await expect(page.getByTestId(`backlog-story-points-button-${fixture.item.id}`)).toContainText(
			'5',
			{ timeout: 20_000 },
		);
	} finally {
		await cleanup(request, fixture);
	}
});
