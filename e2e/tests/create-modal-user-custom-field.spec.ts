import { expect, test, type Page } from '../fixtures/context-path';
import { createCustomFieldViaAPI } from '../fixtures/api-helpers';
import {
	createGatedWorkspaceWithSessions,
	disposeGatedWorkspace,
	type GatedWorkspace,
} from '../fixtures/role-sessions';

/**
 * GH #420: a user custom field on the create screen is a free picker. It must
 * resolve its roster from the workspace-scoped assignable-users endpoint so an
 * Editor (who has item.create + users:read but not the global user.list
 * permission) can pick a workspace member. The global /users directory 403s
 * for that role, which previously left the picker empty.
 */

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

async function openCreateModal(page: Page, workspaceId: number) {
	await page.goto(`/workspaces/${workspaceId}/backlog`);
	await page.locator('#global-create-button').click();
	await expect(page.locator('#work-item-title')).toBeVisible({ timeout: 5000 });
}

test.describe('create modal user custom field (GH #420)', () => {
	test('editor picks a workspace member from a user custom field', async ({ request, browser }) => {
		test.setTimeout(90_000);
		const suffix = `cmu${Date.now()}`;
		let fixture: GatedWorkspace | undefined;
		let fieldId: number | undefined;
		let screenId: number | undefined;
		let configSetId: number | undefined;
		try {
			fixture = await createGatedWorkspaceWithSessions(request, browser, suffix, [
				{ label: 'editor', role: 'Editor' },
			]);
			const { workspaceId, sessionsByLabel } = fixture;
			const editor = sessionsByLabel.editor;

			const field = await createCustomFieldViaAPI(request, {
				name: `Reviewer ${suffix}`,
				field_type: 'user',
				required: false,
			});
			fieldId = field.id;

			const screenResponse = await request.post('/api/screens', {
				headers: SEC_FETCH,
				data: { name: `User CF screen ${suffix}`, description: 'e2e' },
			});
			expect(screenResponse.ok()).toBeTruthy();
			const screen = await screenResponse.json();
			screenId = screen.id;
			const fieldsResponse = await request.put(`/api/screens/${screen.id}/fields`, {
				headers: SEC_FETCH,
				data: [
					{
						field_type: 'custom',
						field_identifier: String(field.id),
						display_order: 0,
						is_required: false,
						field_width: 'full',
					},
				],
			});
			expect(fieldsResponse.ok()).toBeTruthy();

			const configSetResponse = await request.post('/api/configuration-sets', {
				headers: SEC_FETCH,
				data: {
					name: `User CF config ${suffix}`,
					description: 'e2e',
					workspace_ids: [workspaceId],
					create_screen_id: screen.id,
					edit_screen_id: screen.id,
					view_screen_id: screen.id,
				},
			});
			expect(configSetResponse.ok()).toBeTruthy();
			const configSet = await configSetResponse.json();
			configSetId = configSet.id;

			const page = editor.page;
			await openCreateModal(page, workspaceId);

			const toggle = page.getByTestId('create-additional-fields-toggle');
			await expect(toggle).toBeVisible({ timeout: 10_000 });
			await toggle.click();

			// The custom field's own UserPicker uses the default trigger testid.
			await page.getByTestId('user-picker-trigger').click();
			await expect(page.getByTestId(`user-picker-option-${editor.id}`)).toBeVisible();
		} finally {
			if (configSetId != null) {
				await request
					.delete(`/api/configuration-sets/${configSetId}`, { headers: SEC_FETCH })
					.catch(() => {});
			}
			if (screenId != null) {
				await request.delete(`/api/screens/${screenId}`, { headers: SEC_FETCH }).catch(() => {});
			}
			if (fieldId != null) {
				await request
					.delete(`/api/v2/custom-fields/${fieldId}`, { headers: SEC_FETCH })
					.catch(() => {});
			}
			if (fixture) await disposeGatedWorkspace(request, fixture);
		}
	});
});
