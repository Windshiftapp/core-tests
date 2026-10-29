import {
	authenticateAdminRequest,
	createItemViaAPI,
	createWorkspaceViaAPI,
} from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/errors';

test.describe('ticket merge lifecycle', () => {
	test('merging a duplicate moves its content and leaves a durable redirect', async ({
		page,
		request,
		allowConsoleError,
	}, testInfo) => {
		allowConsoleError(/\/api\/logbook\//);
		allowConsoleError(/\/api\/items\/\d+\/recurrence/);

		const stamp = `${Date.now()}${testInfo.workerIndex}${testInfo.repeatEachIndex}`;
		await authenticateAdminRequest(request);
		const workspace = await createWorkspaceViaAPI(request, {
			name: `ticket-merge-${stamp}`,
			key: `TM${stamp.slice(-6)}`.toUpperCase(),
			description: 'ticket merge e2e',
		});
		const canonical = await createItemViaAPI(request, workspace.id, {
			title: `Canonical request ${stamp}`,
		});
		const duplicate = await createItemViaAPI(request, workspace.id, {
			title: `Duplicate request ${stamp}`,
		});

		const commentResponse = await request.post(
			`${process.env.BASE_URL || 'http://localhost:8080'}/api/v2/items/${duplicate.id}/comments`,
			{
				data: { content: 'duplicate detail worth keeping', is_private: false },
			},
		);
		expect(commentResponse.ok()).toBeTruthy();

		// Open the canonical ticket and merge the duplicate via the work-item picker.
		await page.goto(`/workspaces/${workspace.id}/items/${canonical.id}`);
		await expect(page.getByTestId('item-detail-ready')).toBeVisible({ timeout: 10_000 });

		await page.getByTestId('item-detail-actions-menu').click();
		await page.getByTestId('item-merge-open').click();

		const dialog = page.getByTestId('item-merge-dialog');
		await dialog.getByTestId('item-picker-trigger').click();
		// The picker menu is portalled to <body>, outside the dialog subtree.
		await page.getByTestId('item-merge-duplicate-search').fill(`Duplicate request ${stamp}`);
		await page.getByTestId(`item-merge-duplicate-option-${duplicate.id}`).click();
		await page.getByTestId('item-merge-confirm').click();
		await expect(page.getByTestId('item-merge-confirm')).toBeHidden({ timeout: 10_000 });

		// The moved comment is now on the canonical ticket.
		await expect(
			page.getByTestId('comments-section').getByText('duplicate detail worth keeping'),
		).toBeVisible({ timeout: 10_000 });

		// The duplicate shows a durable redirect back to the canonical.
		await page.goto(`/workspaces/${workspace.id}/items/${duplicate.id}`);
		await expect(page.getByTestId('item-detail-ready')).toBeVisible({ timeout: 10_000 });
		const banner = page.getByTestId('item-merged-banner');
		await expect(banner).toBeVisible();

		// The redirect link opens the canonical ticket.
		await page.getByTestId('item-merged-banner-link').click();
		await expect(page.getByTestId('item-title-edit')).toHaveText(canonical.title, {
			timeout: 10_000,
		});
	});
});

test.describe('ticket split lifecycle', () => {
	test('splitting selected comments creates a child subticket', async ({
		page,
		request,
		allowConsoleError,
	}, testInfo) => {
		allowConsoleError(/\/api\/logbook\//);
		allowConsoleError(/\/api\/items\/\d+\/recurrence/);

		const stamp = `${Date.now()}${testInfo.workerIndex}${testInfo.repeatEachIndex}`;
		await authenticateAdminRequest(request);
		const workspace = await createWorkspaceViaAPI(request, {
			name: `ticket-split-${stamp}`,
			key: `TS${stamp.slice(-6)}`.toUpperCase(),
			description: 'ticket split e2e',
		});
		const source = await createItemViaAPI(request, workspace.id, {
			title: `Complex request ${stamp}`,
		});
		let childId = 0;

		for (const content of ['stays behind', 'moves out']) {
			const response = await request.post(
				`${process.env.BASE_URL || 'http://localhost:8080'}/api/v2/items/${source.id}/comments`,
				{ data: { content, is_private: false } },
			);
			expect(response.ok()).toBeTruthy();
		}

		await page.goto(`/workspaces/${workspace.id}/items/${source.id}`);
		await expect(page.getByTestId('item-detail-ready')).toBeVisible({ timeout: 10_000 });

		await page.getByTestId('item-detail-actions-menu').click();
		await page.getByTestId('item-split-open').click();

		await page.getByTestId('item-split-title-input').fill('Follow-up request');
		// The queue renders newest-first; select by content, not position.
		const row = page.locator('label').filter({ hasText: 'moves out' });
		await row.locator('input[type="checkbox"]').check();
		await page.getByTestId('item-split-confirm').click();

		// The app navigates to the new subticket. Reload so the comments tab
		// re-fetches instead of racing the split transaction's completion.
		await expect(page.getByTestId('item-title-edit')).toHaveText('Follow-up request', {
			timeout: 10_000,
		});
		childId = Number(page.url().split('/items/')[1].split(/[/?#]/)[0]);
		await page.reload();
		await expect(page.getByTestId('item-detail-ready')).toBeVisible({ timeout: 10_000 });
		await expect(page.getByTestId('comments-section').getByText('moves out')).toBeVisible();
	});
});
