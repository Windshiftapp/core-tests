import { expect, test } from '../fixtures/context-path';
import { authenticateAdminRequest } from '../fixtures/api-helpers';
import {
	attachRequestTypesToSection,
	createPortalChannel,
	createSimpleRequestType,
} from '../helpers/portal-setup';

const BASE_URL = process.env.BASE_URL || 'http://localhost:8080';
const SEC_FETCH = { 'Sec-Fetch-Site': 'same-origin' };

interface PortalTicket {
	workspaceId: number;
	itemId: number;
}

// Intake plumbing: portal channel + request type created first, submission
// separate so workspace configuration (SLA metrics) lands before the
// customer acts.
async function createPortal(
	request: import('@playwright/test').APIRequestContext,
	slug: string,
): Promise<{ workspaceId: number; channelId: number; requestTypeId: number }> {
	const portal = await createPortalChannel(request, { slug, name: `Acceptance ${slug}` });
	const requestType = await createSimpleRequestType(request, portal, {
		name: `Acceptance request ${slug}`,
	});
	await attachRequestTypesToSection(request, portal, [requestType.id]);
	return {
		workspaceId: portal.workspaceId,
		channelId: portal.channelId,
		requestTypeId: requestType.id,
	};
}

async function submitPortalTicket(
	request: import('@playwright/test').APIRequestContext,
	slug: string,
	requestTypeId: number,
	title: string,
): Promise<PortalTicket> {
	const submit = await request.post(`${BASE_URL}/api/portal/${slug}/submit`, {
		headers: SEC_FETCH,
		data: {
			request_type_id: requestTypeId,
			title,
			description: 'Acceptance suite submission.',
		},
	});
	expect(
		submit.ok(),
		`portal submit: ${submit.status()} ${await submit.text()}`,
	).toBeTruthy();
	const itemId = (await submit.json()).item_id as number;
	expect(itemId).toBeGreaterThan(0);
	return { workspaceId: 0, itemId };
}

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
	expect(open && done, 'default Open/Done statuses exist').toBeTruthy();
	return { open: open.id, done: done.id };
}

async function createFirstResponseMetric(
	request: import('@playwright/test').APIRequestContext,
	workspaceID: number,
	stamp: string,
): Promise<void> {
	const calendarResponse = await request.post(
		`${BASE_URL}/api/v2/workspaces/${workspaceID}/sla/calendars`,
		{ headers: SEC_FETCH, data: { name: `Acceptance hours ${stamp}`, timezone: 'UTC' } },
	);
	expect(calendarResponse.ok(), 'create SLA calendar').toBeTruthy();
	const calendar = (await calendarResponse.json()).data;

	const metricResponse = await request.post(
		`${BASE_URL}/api/v2/workspaces/${workspaceID}/sla/metrics`,
		{
			headers: SEC_FETCH,
			data: {
				name: `First response ${stamp}`,
				display_format: 'time',
				is_active: true,
				conditions: [
					{ phase: 'start', position: 0, condition_type: 'created', config: {} },
				],
				goals: [
					{
						position: 0,
						ql_query: '1 = 1',
						import_status: 'native',
						targets: [
							{ position: 0, target_ms: 3_600_000, calendar_id: calendar.id },
						],
					},
				],
			},
		},
	);
	expect(
		metricResponse.ok(),
		`create SLA metric: ${metricResponse.status()} ${await metricResponse.text()}`,
	).toBeTruthy();
}

/**
 * Release acceptance for the helpdesk loop (WI-1142): customer form intake,
 * triage queues, agent reply, SLA outcome, reporting, the audit trail, and
 * permission + workspace isolation.
 */
test.describe('helpdesk release acceptance', () => {
	test('covers form intake through triage, reply, SLA outcome, reporting, and audit', async ({
		page,
		request,
	}, testInfo) => {
		const stamp = `${Date.now()}${testInfo.workerIndex}${testInfo.repeatEachIndex}`;
		await authenticateAdminRequest(request);

		// SLA configuration lands before intake so the created fact opens a
		// one-hour cycle.
		const portal = await createPortal(request, `accept-${stamp.slice(-8)}`);
		await createFirstResponseMetric(request, portal.workspaceId, stamp);

		const { itemId: ticketID } = await submitPortalTicket(
			request,
			`accept-${stamp.slice(-8)}`,
			portal.requestTypeId,
			`Acceptance ticket ${stamp}`,
		);

		// Agent reply to the customer, public.
		const reply = await request.post(`${BASE_URL}/api/v2/items/${ticketID}/comments`, {
			headers: SEC_FETCH,
			data: { content: 'We are on it — acceptance reply.', is_private: false },
		});
		expect(reply.ok(), `agent reply: ${reply.status()}`).toBeTruthy();

		// SLA outcome: advance the test clock past the goal.
		const clock = await request.post(`${BASE_URL}/api/test/sla/clock`, {
			data: { advance_ms: 2 * 60 * 60 * 1000 },
		});
		expect(clock.ok(), 'advance SLA test clock').toBeTruthy();

		const slaAfter = await request.get(`${BASE_URL}/api/v2/items/${ticketID}/sla`);
		const afterText = JSON.stringify(await slaAfter.json());
		expect(
			afterText.includes('"breached":true') || afterText.includes('Breached'),
			`SLA state flips to breached after the clock advance: ${afterText.slice(0, 400)}`,
		).toBeTruthy();

		// Resolution through the transition endpoint.
		const statuses = await lookupStatuses(request, portal.workspaceId);
		const transition = await request.post(`${BASE_URL}/api/v2/items/${ticketID}/transition`, {
			headers: SEC_FETCH,
			data: { to_status_id: statuses.done },
		});
		expect(transition.ok(), `transition: ${transition.status()}`).toBeTruthy();

		// Reporting: the aggregate reflects the whole loop.
		const today = new Date();
		const weekAgo = new Date(today);
		weekAgo.setDate(weekAgo.getDate() - 7);
		const from = weekAgo.toISOString().slice(0, 10);
		const to = today.toISOString().slice(0, 10);
		const metrics = await request.get(
			`${BASE_URL}/api/v2/support/metrics/aggregate?workspace_id=${portal.workspaceId}&from=${from}&to=${to}`,
		);
		expect(metrics.ok(), 'support metrics aggregate').toBeTruthy();
		const doc = (await metrics.json()).data ?? (await metrics.json());
		expect(doc.summary.created).toBe(1);
		expect(doc.summary.resolved).toBe(1);
		expect(doc.summary.first_response.count).toBe(1);
		expect(doc.summary.sla.breached).toBe(0);
		expect(doc.summary.sla.ongoing_breached).toBe(1);
		expect(
			(doc.queues ?? []).some((queue: { key: string }) => queue.key === 'unassigned'),
			'queue catalog present with live counts for workspace scope',
		).toBeTruthy();

		// Browser-visible outcome: the metrics widget on the homepage.
		const layout = await request.put(
			`${BASE_URL}/api/workspaces/${portal.workspaceId}/homepage/layout`,
			{
				data: {
					sections: [
						{
							id: 'acceptance-support',
							title: 'Support',
							display_order: 0,
							widget_ids: ['acceptance-support-widget'],
						},
					],
					widgets: [
						{
							id: 'acceptance-support-widget',
							type: 'support-metrics',
							section_id: 'acceptance-support',
							position: 0,
							width: 2,
							config: { window: '28d' },
						},
					],
				},
			},
		);
		expect(layout.ok(), 'install support-metrics widget').toBeTruthy();

		await page.goto(`/workspaces/${portal.workspaceId}/overview`);
		const backlog = page.getByTestId('support-metrics-backlog');
		await expect(backlog).toBeVisible({ timeout: 15_000 });
		await expect(backlog).toContainText('0');

		// Audit trail: operator-visible decisions.
		const audit = await request.get(
			`${BASE_URL}/api/admin/audit-logs?action_type=comment.create&per_page=50`,
		);
		expect(audit.ok(), 'read audit log').toBeTruthy();
		const auditRows = JSON.stringify(await audit.json());
		expect(auditRows).toContain('comment.create');
		expect(auditRows).toContain('customer_reply');
	});

	test('isolates workspaces and enforces permissions on acceptance surfaces', async ({
		request,
	}, testInfo) => {
		const stamp = `${Date.now()}${testInfo.workerIndex}`;
		await authenticateAdminRequest(request);

		// Workspace isolation: each portal workspace reports only its own
		// tickets.
		const first = await createPortal(request, `iso-a-${stamp.slice(-8)}`);
		const second = await createPortal(request, `iso-b-${stamp.slice(-8)}`);
		await submitPortalTicket(
			request,
			`iso-a-${stamp.slice(-8)}`,
			first.requestTypeId,
			`Isolation ticket A ${stamp}`,
		);
		await submitPortalTicket(
			request,
			`iso-b-${stamp.slice(-8)}`,
			second.requestTypeId,
			`Isolation ticket B ${stamp}`,
		);

		const today = new Date();
		const weekAgo = new Date(today);
		weekAgo.setDate(weekAgo.getDate() - 7);
		const from = weekAgo.toISOString().slice(0, 10);
		const to = today.toISOString().slice(0, 10);

		const firstMetrics = await request.get(
			`${BASE_URL}/api/v2/support/metrics/aggregate?workspace_id=${first.workspaceId}&from=${from}&to=${to}`,
		);
		const firstDoc = (await firstMetrics.json()).data ?? (await firstMetrics.json());
		expect(firstDoc.summary.created).toBe(1);

		const secondMetrics = await request.get(
			`${BASE_URL}/api/v2/support/metrics/aggregate?workspace_id=${second.workspaceId}&from=${from}&to=${to}`,
		);
		const secondDoc = (await secondMetrics.json()).data ?? (await secondMetrics.json());
		expect(secondDoc.summary.created).toBe(1);
		expect(secondDoc.summary.backlog).toBe(1);

		// Unknown workspace is 404 (no existence leak).
		const missing = await request.get(
			`${BASE_URL}/api/v2/support/metrics/aggregate?workspace_id=99999999`,
		);
		expect(missing.status()).toBe(404);

		// Anonymous callers cannot read the audit trail. Raw fetch shares
		// nothing with the authenticated Playwright context.
		const anon = await fetch(`${BASE_URL}/api/admin/audit-logs`);
		expect(
			[401, 403],
			`anonymous audit-log access must be denied, got ${anon.status}`,
		).toContain(anon.status);
	});
});
