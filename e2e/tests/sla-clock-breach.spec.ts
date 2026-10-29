import { expect, test } from '../fixtures/context-path';
import {
	authenticateAdminRequest,
	createItemViaAPI,
	createWorkspaceViaAPI,
} from '../fixtures/api-helpers';
import { generateWorkspace } from '../fixtures/test-data';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const headers = { 'Sec-Fetch-Site': 'same-origin' };

/**
 * SLA display does not depend on background work: the item detail reads derive
 * breach state from the server clock, and the e2e test hook advances that clock
 * with the due-work loop disabled. This spec configures a metric, opens the
 * item, and proves the badge flips to breached only after the clock moves.
 */
test.describe('SLA clock breach', () => {
	test('advancing the server clock flips the item SLA state to breached', async ({
		page,
		request,
	}) => {
		await authenticateAdminRequest(request);
		const workspace = await createWorkspaceViaAPI(request, generateWorkspace());

		const calendarName = `Hours ${Date.now()}`;
		const calendarResponse = await request.post(
			`${BASE_URL}/api/v2/workspaces/${workspace.id}/sla/calendars`,
			{ headers, data: { name: calendarName, timezone: 'UTC' } },
		);
		expect(
			calendarResponse.ok(),
			`create calendar failed (${calendarResponse.status()}): ${await calendarResponse.text()}`,
		).toBeTruthy();
		const calendar = (await calendarResponse.json()).data;

		const metricName = `First response ${Date.now()}`;
		const metricResponse = await request.post(
			`${BASE_URL}/api/v2/workspaces/${workspace.id}/sla/metrics`,
			{
				headers,
				data: {
					name: metricName,
					display_format: 'time',
					is_active: true,
					conditions: [
						{
							phase: 'start',
							position: 0,
							condition_type: 'created',
							config: {},
						},
					],
					goals: [
						{
							position: 0,
							ql_query: '1 = 1',
							import_status: 'native',
							targets: [
								{
									position: 0,
									target_ms: 3_600_000,
									calendar_id: calendar.id,
								},
							],
						},
					],
				},
			},
		);
		expect(
			metricResponse.ok(),
			`create metric failed (${metricResponse.status()}): ${await metricResponse.text()}`,
		).toBeTruthy();
		const metric = (await metricResponse.json()).data;

		// Creating the item records a created fact, so inline evaluation opens a
		// one-hour cycle with the due-work loop disabled.
		const item = await createItemViaAPI(request, workspace.id, {
			title: `SLA clock item ${Date.now()}`,
		});

		await page.goto(`/workspaces/${workspace.id}/items/${item.id}`);
		await expect(page.getByTestId('item-detail-ready')).toBeVisible();
		await expect(page.getByTestId('item-sla-panel')).toBeVisible();

		const metricBlock = page.getByTestId(`item-sla-metric-${metric.id}`);
		await expect(metricBlock).toContainText(metricName);
		await expect(metricBlock.getByText('Breached')).toHaveCount(0);

		const clockResponse = await request.post(`${BASE_URL}/api/test/sla/clock`, {
			data: { advance_ms: 2 * 60 * 60 * 1000 },
		});
		expect(
			clockResponse.ok(),
			`advance clock failed (${clockResponse.status()}): ${await clockResponse.text()}`,
		).toBeTruthy();

		await page.reload();
		await expect(page.getByTestId(`item-sla-metric-${metric.id}`).getByText('Breached')).toBeVisible();
	});
});
