import { describe, expect, it } from 'vitest';
import {
	buildBoardConfigurationPayload,
	legacyFieldsForCompletedVisibility,
} from './boardConfigPayload.js';

describe('board configuration payload', () => {
	it('preserves loaded board settings and adds milestone swimlanes', () => {
		const config = {
			columns: [
				{ id: 7, name: 'Doing', display_order: 4, wip_limit: 3, color: '#123456', status_ids: [11, 12] },
			],
			backlog_status_ids: [3],
			list_columns: [{ field_identifier: 'title', field_type: 'system', display_order: 0, width: 2 }],
			roadmap_config: { start_field_id: 'a', end_field_id: 'b' },
			card_fields: [{ field_identifier: 'milestone', field_type: 'system', display_order: 2, width: 1 }],
			show_rightmost_column_last_50: true,
			completed_item_retention_days: null,
			view_settings: { enabled_views: ['board'] },
		};

		const payload = buildBoardConfigurationPayload(config, {
			globalMilestoneIds: [9, 4, 9],
			showCompleted: false,
			showOpenFromCompleted: true,
		});

		expect(payload.columns).toEqual([
			{ id: 7, name: 'Doing', display_order: 0, wip_limit: 3, color: '#123456', status_ids: [11, 12] },
		]);
		expect(payload.backlog_status_ids).toEqual([3]);
		expect(payload.list_columns).toEqual([
			{ field_identifier: 'title', field_type: 'system', display_order: 0, width: 2 },
		]);
		expect(payload.roadmap_config).toEqual({ start_field_id: 'a', end_field_id: 'b' });
		expect(payload.card_fields).toEqual([
			{ field_identifier: 'milestone', field_type: 'system', display_order: 0, width: 1 },
		]);
		expect(payload.show_rightmost_column_last_50).toBe(true);
		expect(payload.completed_item_retention_days).toBeNull();
		expect(payload.view_settings).toEqual({
			milestone_swimlanes: {
				global_milestone_ids: [9, 4],
				show_completed: false,
				show_open_from_completed: true,
			},
		});
		// enabled_views is intentionally omitted so the backend keeps the stored
		// navigation override instead of pinning or clearing it.
		expect(payload.view_settings).not.toHaveProperty('enabled_views');
	});

	it('defaults to showing completed milestones and no pinned globals', () => {
		const payload = buildBoardConfigurationPayload(null, {});

		expect(payload.columns).toEqual([]);
		expect(payload.card_fields).toEqual([]);
		expect(payload.show_rightmost_column_last_50).toBe(false);
		expect(payload.view_settings).toEqual({
			milestone_swimlanes: {
				global_milestone_ids: [],
				show_completed: true,
				show_open_from_completed: false,
			},
		});
	});

	it('maps each completed-visibility preset onto the legacy fields', () => {
		expect(legacyFieldsForCompletedVisibility('last_day')).toEqual({
			showRightmostColumnLast50: false,
			completedItemRetentionDays: 1,
		});
		expect(legacyFieldsForCompletedVisibility('last_week')).toEqual({
			showRightmostColumnLast50: false,
			completedItemRetentionDays: 7,
		});
		expect(legacyFieldsForCompletedVisibility('all')).toEqual({
			showRightmostColumnLast50: false,
			completedItemRetentionDays: null,
		});
		expect(legacyFieldsForCompletedVisibility('none')).toEqual({
			showRightmostColumnLast50: false,
			completedItemRetentionDays: null,
		});
	});

	it('persists an explicit completed-visibility preset and clears the cap', () => {
		const payload = buildBoardConfigurationPayload(
			{ show_rightmost_column_last_50: true, completed_item_retention_days: 30 },
			{ completedVisibility: 'last_week' }
		);

		expect(payload.show_rightmost_column_last_50).toBe(false);
		expect(payload.completed_item_retention_days).toBe(7);
		expect(payload.view_settings.completed_visibility).toBe('last_week');
	});

	it('round-trips a stored preset and keeps its legacy fields aligned', () => {
		const payload = buildBoardConfigurationPayload({
			show_rightmost_column_last_50: false,
			completed_item_retention_days: null,
			view_settings: { completed_visibility: 'current_iteration' },
		});

		expect(payload.view_settings.completed_visibility).toBe('current_iteration');
		expect(payload.show_rightmost_column_last_50).toBe(false);
		expect(payload.completed_item_retention_days).toBeNull();
	});
});
