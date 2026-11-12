import type { BrowserContext, APIRequestContext } from '@playwright/test';
import {
	createItemViaAPI,
	createUserViaAPI,
	createWorkspaceViaAPI,
} from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/context-path';
import { generateUser, generateWorkspace } from '../fixtures/test-data';

const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

const ARABIC_TITLE = 'عنوان عربي للاختبار';
const ENGLISH_TITLE = 'English title stays left aligned';

async function grantSystemAdmin(request: APIRequestContext, userId: number) {
	const permissionsResponse = await request.get('/api/permissions', {
		headers: SEC_FETCH,
	});
	expect(permissionsResponse.ok()).toBeTruthy();
	const permissions = await permissionsResponse.json();
	const systemAdmin = permissions.find(
		(permission: { permission_key: string }) =>
			permission.permission_key === 'system.admin',
	);
	expect(systemAdmin, 'system.admin permission').toBeDefined();
	const grant = await request.post('/api/permissions/global/grant', {
		headers: SEC_FETCH,
		data: { user_id: userId, permission_id: systemAdmin.id },
	});
	expect(grant.ok()).toBeTruthy();
}

function titleLocator(page: import('@playwright/test').Page) {
	return page
		.getByTestId('item-title-edit')
		.or(page.getByTestId('item-title-readonly'));
}

/**
 * Computed `text-align` stays logical (`start`), so also measure where the text
 * actually sits inside the element: an RTL run hugs the right edge, an LTR run
 * the left.
 */
async function readTitleAlignment(
	page: import('@playwright/test').Page,
) {
	return titleLocator(page).evaluate((el) => {
		const range = document.createRange();
		range.selectNodeContents(el);
		const textRect = range.getBoundingClientRect();
		const elRect = el.getBoundingClientRect();
		return {
			direction: getComputedStyle(el).direction,
			textAlign: getComputedStyle(el).textAlign,
			leftGap: textRect.left - elRect.left,
			rightGap: elRect.right - textRect.right,
		};
	});
}

test.describe('RTL / LTR bidi text placement', () => {
	test('content follows its own direction in an RTL interface', async ({
		browser,
		request,
	}) => {
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace('rtl-bidi'),
		);
		const arabicItem = await createItemViaAPI(request, workspace.id, {
			title: ARABIC_TITLE,
		});
		const englishItem = await createItemViaAPI(request, workspace.id, {
			title: ENGLISH_TITLE,
		});

		const userData = generateUser('rtl-bidi');
		const admin = await createUserViaAPI(request, userData);
		await grantSystemAdmin(request, admin.id);
		const regional = await request.put(
			`/api/users/${admin.id}/regional-settings`,
			{
				headers: SEC_FETCH,
				data: { language: 'ar', timezone: 'UTC' },
			},
		);
		expect(regional.ok()).toBeTruthy();

		let context: BrowserContext | undefined;
		try {
			context = await browser.newContext({ baseURL: process.env.BASE_URL });
			const login = await context.request.post('/api/auth/login', {
				headers: SEC_FETCH,
				data: {
					email_or_username: userData.username,
					password: userData.password_hash,
					remember_me: false,
				},
			});
			expect(login.ok()).toBeTruthy();
			const page = await context.newPage();

			// Arabic UI + Arabic title: the title must sit on the right.
			await page.goto(`/workspaces/${workspace.id}/items/${arabicItem.id}`);
			await expect(page.getByTestId('item-detail-ready')).toBeVisible();
			await expect
				.poll(() => page.evaluate(() => document.documentElement.dir))
				.toBe('rtl');

			const arabicTitle = titleLocator(page);
			await expect(arabicTitle).toHaveAttribute('dir', 'auto');
			const arabic = await readTitleAlignment(page);
			expect(arabic.direction).toBe('rtl');
			expect(arabic.textAlign).toBe('start');
			expect(arabic.rightGap).toBeLessThan(arabic.leftGap);

			// Arabic UI + English title: the title must stay left-to-right.
			await page.goto(`/workspaces/${workspace.id}/items/${englishItem.id}`);
			await expect(page.getByTestId('item-detail-ready')).toBeVisible();

			const englishTitle = titleLocator(page);
			await expect(englishTitle).toHaveAttribute('dir', 'auto');
			const english = await readTitleAlignment(page);
			expect(english.direction).toBe('ltr');
			expect(english.textAlign).toBe('start');
			expect(english.leftGap).toBeLessThan(english.rightGap);
		} finally {
			await context?.close();
			await request.delete(`/api/users/${admin.id}`, { headers: SEC_FETCH });
			await request.delete(`/api/v2/workspaces/${workspace.id}`, {
				headers: SEC_FETCH,
			});
		}
	});

	test('RTL content renders right-to-left in an LTR interface', async ({
		page,
		request,
	}) => {
		const workspace = await createWorkspaceViaAPI(
			request,
			generateWorkspace('ltr-bidi'),
		);
		try {
			const arabicItem = await createItemViaAPI(request, workspace.id, {
				title: ARABIC_TITLE,
			});

			await page.goto(`/workspaces/${workspace.id}/items/${arabicItem.id}`);
			await expect(page.getByTestId('item-detail-ready')).toBeVisible();
			await expect
				.poll(() => page.evaluate(() => document.documentElement.dir))
				.toBe('ltr');

			const title = titleLocator(page);
			await expect(title).toHaveAttribute('dir', 'auto');
			const arabic = await readTitleAlignment(page);
			expect(arabic.direction).toBe('rtl');
			expect(arabic.textAlign).toBe('start');
			expect(arabic.rightGap).toBeLessThan(arabic.leftGap);
		} finally {
			await request.delete(`/api/v2/workspaces/${workspace.id}`, {
				headers: SEC_FETCH,
			});
		}
	});
});
