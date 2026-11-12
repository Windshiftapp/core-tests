import { render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api/sla.js', () => ({
	sla: {
		getItemSLA: vi.fn(),
		getItemSLABatch: vi.fn(),
		getWarningThresholds: vi.fn(),
	},
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
	t: (key, params) => (params ? `${key} ${JSON.stringify(params)}` : key),
}));

vi.mock('../../utils/dateFormatter.js', () => ({
	formatInstant: (_value, timezone, options) => `${timezone} ${options.hour}:${options.minute}`,
}));

import { sla } from '../../api/sla.js';
import ItemSLAPanel from '../items/ItemSLAPanel.svelte';
import SLABadge from './SLABadge.svelte';
import { resetSLAState } from './slaState.js';

const state = [
	{
		metric_id: 1,
		metric_name: 'First response',
		display_format: 'time',
		recalculating: false,
		ongoing: {
			cycle_no: 1,
			breached: false,
			paused: false,
			within_calendar_hours: true,
			elapsed_ms: 1000,
			goal_duration_ms: 3600000,
			remaining_ms: 3599000,
		},
		completed: [],
	},
];

describe('SLA refresh reconciliation', () => {
	beforeEach(() => {
		vi.clearAllMocks();
		resetSLAState();
		sla.getWarningThresholds.mockResolvedValue([]);
		sla.getItemSLABatch.mockResolvedValue({ 42: state });
	});

	it('shares one workspace batch between the panel and a badge', async () => {
		render(ItemSLAPanel, { itemId: 42, workspaceId: 5 });
		render(SLABadge, { itemId: 42, workspaceId: 5 });

		await screen.findByTestId('item-sla-panel');
		await vi.waitFor(() => expect(sla.getItemSLABatch).toHaveBeenCalledTimes(1));
		expect(sla.getItemSLABatch).toHaveBeenCalledWith(5, [42], expect.anything());
		expect(sla.getItemSLA).not.toHaveBeenCalled();
	});

	it('reconciles the shared SLA surface once per refresh-work-items event', async () => {
		render(ItemSLAPanel, { itemId: 42, workspaceId: 5 });
		render(SLABadge, { itemId: 42, workspaceId: 5 });
		await screen.findByTestId('item-sla-panel');
		await vi.waitFor(() => expect(sla.getItemSLABatch).toHaveBeenCalledTimes(1));

		sla.getItemSLABatch.mockClear();
		window.dispatchEvent(
			new CustomEvent('refresh-work-items', {
				detail: { itemId: 42, item: { id: 42, workspace_id: 5 } },
			}),
		);

		await vi.waitFor(() => expect(sla.getItemSLABatch).toHaveBeenCalledTimes(1));
		expect(sla.getItemSLABatch).toHaveBeenCalledWith(5, [42], expect.anything());
	});

	it('skips a burst that names another workspace', async () => {
		render(ItemSLAPanel, { itemId: 42, workspaceId: 5 });
		render(SLABadge, { itemId: 42, workspaceId: 5 });
		await screen.findByTestId('item-sla-panel');
		await vi.waitFor(() => expect(sla.getItemSLABatch).toHaveBeenCalledTimes(1));

		sla.getItemSLABatch.mockClear();
		vi.useFakeTimers();
		try {
			window.dispatchEvent(
				new CustomEvent('refresh-work-items', {
					detail: { itemId: 42, item: { id: 42, workspace_id: 99 } },
				}),
			);

			await vi.advanceTimersByTimeAsync(20);
			expect(sla.getItemSLABatch).not.toHaveBeenCalled();
		} finally {
			vi.useRealTimers();
		}
	});
});
