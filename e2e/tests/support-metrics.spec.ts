import { authenticateAdminRequest } from '../fixtures/api-helpers';
import { expect, test } from '../fixtures/errors';
import {
	attachRequestTypesToSection,
	createPortalChannel,
	createSimpleRequestType,
} from '../helpers/portal-setup';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

async function lookupStatuses(
	request: import('@playwright/test').APIRequestContext,
	workspaceID: number,
): Promise<{ open: number; done: number }> {
	const response = await request.get(
		`${BASE_URL}/api/v2/workspaces/${workspaceID}/statuses`,
	);
	expect(response.ok()).toBeTruthy();
	const body = (await response.json()) as { data?: Array<{ id: number; name: string }> };
	const statuses = (Array.isArray(body) ? body : body.data) ?? [];
	const open = statuses.find((status) => status.name === 'Open');
	const done = statuses.find((status) => status.name === 'Done');
	expect(open, 'default Open status exists').toBeTruthy();
	expect(done, 'default Done status exists').toBeTruthy();
	return { open: open.id, done: done.id };
}

async function submitPortalTicket(
	request: import('@playwright/test').APIRequestContext,
	slug: string,
	title: string,
): Promise<{ workspaceId: number; itemId: number }> {
	const portal = await createPortalChannel(request, {
		slug,
		name: `Support Metrics Portal ${slug}`,
	});
	const requestType = await createSimpleRequestType(request, portal, {
		name: `Metrics request ${slug}`,
	});
	await attachRequestTypesToSection(request, portal, [requestType.id]);

	const submit = await request.post(`${BASE_URL}/api/portal/${slug}/submit`, {
		headers: SEC_FETCH,
		data: {
			request_type_id: requestType.id,
			title,
			description: 'Submitted for support metrics coverage.',
		},
	});
	expect(
		submit.ok(),
		`portal submit: ${submit.status()} ${await submit.text()}`,
	).toBeTruthy();
	const itemId = (await submit.json()).item_id as number;
	expect(itemId).toBeGreaterThan(0);
	return { workspaceId: portal.workspaceId, itemId };
}

test.describe('support metrics widgets', () => {
	test('renders live helpdesk numbers on the workspace homepage', async ({
		page,
		request,
	}, testInfo) => {
		const stamp = `${Date.now()}${testInfo.workerIndex}${testInfo.repeatEachIndex}`;
		await authenticateAdminRequest(request);

		const { workspaceId, itemId: ticketID } = await submitPortalTicket(
			request,
			`sm-home-${stamp.slice(-8)}`,
			`Printer smoking ${stamp}`,
		);
		const statuses = await lookupStatuses(request, workspaceId);

		// First response through the production comment path.
		const comment = await request.post(`${BASE_URL}/api/v2/items/${ticketID}/comments`, {
			headers: SEC_FETCH,
			data: { content: 'On it — sending a replacement.', is_private: false },
		});
		expect(comment.ok(), `comment: ${comment.status()}`).toBeTruthy();

		// Install the widget on the workspace homepage via the layout API.
		const layout = await request.put(
			`${BASE_URL}/api/workspaces/${workspaceId}/homepage/layout`,
			{
				data: {
					sections: [
						{
							id: 'support-section',
							title: 'Support',
							display_order: 0,
							widget_ids: ['support-widget'],
						},
					],
					widgets: [
						{
							id: 'support-widget',
							type: 'support-metrics',
							section_id: 'support-section',
							position: 0,
							width: 2,
							config: { window: '28d' },
						},
					],
				},
			},
		);
		expect(layout.ok(), `layout save: ${layout.status()}`).toBeTruthy();

		await page.goto(`/workspaces/${workspaceId}/overview`);
		const summary = page.getByTestId('support-metrics-summary');
		await expect(summary).toBeVisible({ timeout: 15_000 });
		await expect(summary).toContainText('Backlog');
		await expect(page.getByTestId('support-metrics-backlog')).toContainText('1');

		// Resolve the ticket through the transition endpoint; the widget must
		// reflect the new state after a reload of the same layout.
		const transition = await request.post(
			`${BASE_URL}/api/v2/items/${ticketID}/transition`,
			{ headers: SEC_FETCH, data: { to_status_id: statuses.done } },
		);
		expect(transition.ok(), `transition: ${transition.status()}`).toBeTruthy();

		await page.reload();
		await expect(page.getByTestId('support-metrics-backlog')).toContainText('0', {
			timeout: 15_000,
		});
	});

	test('keeps the global support-overview widget unconfigured until workspaces are chosen', async ({
		page,
		request,
	}, testInfo) => {
		const stamp = `${Date.now()}${testInfo.workerIndex}${testInfo.repeatEachIndex}`;
		await authenticateAdminRequest(request);
		const { workspaceId } = await submitPortalTicket(
			request,
			`sm-global-${stamp.slice(-8)}`,
			`Global metrics ticket ${stamp}`,
		);

		const layout = await request.put(`${BASE_URL}/api/user/dashboard-layout`, {
			data: {
				grid_columns: 12,
				sections: [
					{
						id: 'global-support',
						title: 'Support',
						display_order: 0,
						widget_ids: ['global-support-widget'],
					},
				],
				widgets: [
					{
						id: 'global-support-widget',
						type: 'support-overview',
						section_id: 'global-support',
						position: 0,
						width: 4,
						config: {},
					},
				],
			},
		});
		expect(layout.ok(), `layout save: ${layout.status()}`).toBeTruthy();

		await page.addInitScript(() => {
			localStorage.setItem('windshift-dashboard-onboarding-dismissed', 'true');
		});
		await page.goto('/homepage');
		const setup = page.getByTestId('support-overview-setup');
		await expect(setup).toBeVisible({ timeout: 15_000 });

		// Choosing the first workspace persists the scope and renders metrics. The
		// layout save is debounced, so wait for it before reloading to prove the
		// scope was actually persisted.
		const layoutSave = page.waitForResponse(
			(response) =>
				response.request().method() === 'PUT' &&
				response.url().endsWith('/api/user/dashboard-layout') &&
				response.ok(),
		);
		await setup.locator('button').first().click();
		await layoutSave;
		await expect(page.getByTestId('support-overview-setup')).toBeHidden({
			timeout: 15_000,
		});
		await expect(page.getByText('dashboard.states.supportMetricsLoadError')).toBeHidden();

		// Reload proves the chosen scope was persisted with the layout.
		await page.reload();
		await expect(page.getByTestId('support-overview-setup')).toBeHidden({
			timeout: 15_000,
		});
	});
});
