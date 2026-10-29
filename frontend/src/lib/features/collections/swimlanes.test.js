import { describe, expect, it } from 'vitest';
import { buildSwimlanes, childrenForLane, itemLaneKeys, laneDropUpdate, laneKeyForItem, mapSwimlaneStorageKey, milestoneAssignableToWorkspace, milestoneDropRemovals, primaryLaneKeyForItem, primaryMilestoneForItem, sortMilestonesForLanes, statusCategoryLaneKey, SWIMLANE_NONE_KEY } from './swimlanes.js';

const statuses = [
	{ id: 1, name: 'To Do', display_name: 'To Do', category_id: 10 },
	{ id: 2, name: 'In Progress', display_name: 'In Progress', category_id: 10 },
	{ id: 3, name: 'Done', display_name: 'Done', category_id: 11 },
];

const statusCategories = [
	{ id: 10, name: 'To Do', display_name: 'To Do', color: '#dae2f5' },
	{ id: 11, name: 'Complete', display_name: 'Complete', color: '#b3dfd1' },
];

const users = [
	{ id: 7, full_name: 'Ada Lovelace' },
	{ id: 8, full_name: 'Alan Turing' },
];

const priorities = [
	{ id: 30, name: 'High', display_name: 'High', color: '#f5ca3f' },
	{ id: 31, name: 'Low', display_name: 'Low', color: '#93a7c4' },
];

const iterations = [
	{ id: 40, name: 'Sprint 2', start_date: '2026-10-01', type_name: 'Sprint', type_color: '#3b82f6' },
	{ id: 41, name: 'Sprint 1', start_date: '2026-09-15', type_name: 'Sprint', type_color: '#3b82f6' },
];

const milestones = [
	{ id: 50, name: 'GA', target_date: '2026-12-01', category_color: '#a1f0d1' },
	{ id: 51, name: 'Beta', target_date: '2026-11-01', category_color: '#f5d0c0' },
];

const item = (overrides = {}) => ({
	id: 1,
	status_id: 1,
	assignee_id: 7,
	priority_id: 30,
	iteration_id: 41,
	milestones: [{ id: 51 }],
	...overrides,
});

describe('map swimlanes', () => {
	it('keys lane membership off the dimension attribute', () => {
		expect(laneKeyForItem(item(), 'status')).toBe('status-1');
		expect(laneKeyForItem(item(), 'assignee')).toBe('assignee-7');
		expect(laneKeyForItem(item(), 'priority')).toBe('priority-30');
		expect(laneKeyForItem(item(), 'iteration')).toBe('iteration-41');
		expect(laneKeyForItem(item(), 'milestone')).toBe('milestone-51');
	});

	it('routes items without a value to the none lane', () => {
		expect(laneKeyForItem(item({ assignee_id: null }), 'assignee')).toBe(SWIMLANE_NONE_KEY);
		expect(laneKeyForItem(item({ priority_id: null }), 'priority')).toBe(SWIMLANE_NONE_KEY);
		expect(laneKeyForItem(item({ iteration_id: null }), 'iteration')).toBe(SWIMLANE_NONE_KEY);
		expect(laneKeyForItem(item({ milestones: [] }), 'milestone')).toBe(SWIMLANE_NONE_KEY);
	});

	it('resolves the status category lane through the status record', () => {
		expect(statusCategoryLaneKey(item({ status_id: 3 }), statuses, statusCategories)).toBe('category-11');
		expect(statusCategoryLaneKey(item({ status_id: null }), statuses, statusCategories)).toBe(SWIMLANE_NONE_KEY);
	});

	it('places milestone items in every matching lane', () => {
		const keys = itemLaneKeys(item({ milestones: [{ id: 50 }, { id: 51 }] }), 'milestone');
		expect(keys).toEqual(['milestone-50', 'milestone-51']);
	});

	it('builds lanes in reference order, drops empty ones, and appends the none lane last', () => {
		const items = [
			item({ id: 1, iteration_id: 41 }),
			item({ id: 2, iteration_id: 40 }),
			item({ id: 3, iteration_id: null }),
			item({ id: 4, iteration_id: 41 }),
		];
		const lanes = buildSwimlanes({ dimension: 'iteration', items, iterations, noneTitle: 'No iteration' });
		expect(lanes.map((l) => l.key)).toEqual(['iteration-41', 'iteration-40', SWIMLANE_NONE_KEY]);
		expect(lanes[0].count).toBe(2);
		expect(lanes[0].title).toBe('Sprint 1');
		expect(lanes[1].title).toBe('Sprint 2');
		expect(lanes[2].count).toBe(1);
	});

	it('resolves status category and priority lanes from reference data', () => {
		const lanes = buildSwimlanes({
			dimension: 'status_category',
			items: [item()],
			statuses,
			statusCategories,
			noneTitle: 'None',
		});
		expect(lanes).toHaveLength(1);
		expect(lanes[0]).toMatchObject({ key: 'category-10', count: 1 });

		const priorityLanes = buildSwimlanes({
			dimension: 'priority',
			items: [item({ priority_id: null })],
			priorities,
			noneTitle: 'No priority',
		});
		expect(priorityLanes.map((l) => l.key)).toEqual([SWIMLANE_NONE_KEY]);
	});

	it('counts milestone items in each of their lanes', () => {
		const items = [
			item({ milestones: [{ id: 50 }, { id: 51 }] }),
			item({ id: 2, milestones: [] }),
		];
		const lanes = buildSwimlanes({ dimension: 'milestone', items, milestones, noneTitle: 'No milestone' });
		const ga = lanes.find((l) => l.key === 'milestone-50');
		const beta = lanes.find((l) => l.key === 'milestone-51');
		const none = lanes.find((l) => l.key === SWIMLANE_NONE_KEY);
		expect(ga.count).toBe(1);
		expect(beta.count).toBe(1);
		expect(none.count).toBe(1);
	});

	it('slices column children per lane and counts multi-lane items once per lane', () => {
		const column = [
			item({ id: 1, milestones: [{ id: 50 }, { id: 51 }] }),
			item({ id: 2, milestones: [] }),
		];
		const gaLane = { key: 'milestone-50' };
		const noneLane = { key: SWIMLANE_NONE_KEY };
		expect(childrenForLane(column, gaLane, 'milestone', statuses, statusCategories).map((i) => i.id)).toEqual([1]);
		expect(childrenForLane(column, noneLane, 'milestone', statuses, statusCategories).map((i) => i.id)).toEqual([2]);
		expect(childrenForLane(null, gaLane, 'milestone', statuses, statusCategories)).toEqual([]);
	});

	it('maps lane drops to attribute payloads, transitions for status moves', () => {
		expect(laneDropUpdate({ key: 'iteration-41' }, 'iteration', statuses)).toEqual({ iteration_id: 41 });
		expect(laneDropUpdate({ key: SWIMLANE_NONE_KEY }, 'iteration', statuses)).toEqual({ iteration_id: null });
		expect(laneDropUpdate({ key: 'assignee-7' }, 'assignee', statuses)).toEqual({ assignee_id: 7 });
		expect(laneDropUpdate({ key: 'priority-30' }, 'priority', statuses)).toEqual({ priority_id: 30 });
		expect(laneDropUpdate({ key: 'milestone-50' }, 'milestone', statuses)).toEqual({ milestone_ids: [50] });
		expect(laneDropUpdate({ key: SWIMLANE_NONE_KEY }, 'milestone', statuses)).toEqual({ milestone_ids: [] });
		expect(laneDropUpdate({ key: 'status-2' }, 'status', statuses)).toEqual({ transitionToStatusId: 2 });
		// A category drop transitions to the first status of that category.
		expect(laneDropUpdate({ key: 'category-11' }, 'status_category', statuses)).toEqual({ transitionToStatusId: 3 });
		expect(laneDropUpdate({ key: SWIMLANE_NONE_KEY }, 'status', statuses)).toEqual({});
	});

	it('stores the chosen dimension per workspace or collection scope', () => {
		expect(mapSwimlaneStorageKey('workspace-3')).toBe('map-swimlane-dimension-workspace-3');
		expect(mapSwimlaneStorageKey('collection-9')).toBe('map-swimlane-dimension-collection-9');
	});
});

describe('map swimlanes — milestones', () => {
	it('orders milestone lanes by target date with undated lanes last', () => {
		const lanes = buildSwimlanes({
			dimension: 'milestone',
			items: [{ id: 1, milestones: [{ id: 50 }, { id: 51 }, { id: 52 }] }],
			milestones: [
				{ id: 50, name: 'GA', target_date: '2026-12-01' },
				{ id: 51, name: 'Beta', target_date: '2026-11-01' },
				{ id: 52, name: 'No date', target_date: null },
			],
			noneTitle: 'No milestone',
		});
		expect(lanes.map((lane) => lane.title)).toEqual(['Beta', 'GA', 'No date']);
	});

	it('sorts with name and id as stable tie-breakers', () => {
		const ordered = sortMilestonesForLanes([
			{ id: 3, name: 'Zulu', target_date: '2026-11-01' },
			{ id: 2, name: 'Alpha', target_date: '2026-11-01' },
			{ id: 1, name: 'Alpha', target_date: '2026-11-01' },
		]);
		expect(ordered.map((milestone) => milestone.id)).toEqual([1, 2, 3]);
	});

	it('can include empty reference lanes for board drop targets', () => {
		const lanes = buildSwimlanes({
			dimension: 'milestone',
			items: [],
			milestones: [
				{ id: 50, name: 'GA', target_date: '2026-12-01' },
				{ id: 51, name: 'Beta', target_date: '2026-11-01' },
			],
			noneTitle: 'No milestone',
			includeEmpty: true,
		});
		expect(lanes.map((lane) => lane.key)).toEqual(['milestone-51', 'milestone-50']);
		expect(lanes.every((lane) => lane.count === 0)).toBe(true);
	});

	it('resolves the primary milestone by earliest target date', () => {
		const item = {
			id: 1,
			assignee_id: 7,
			milestones: [
				{ id: 50, name: 'GA', target_date: '2026-12-01' },
				{ id: 51, name: 'Beta', target_date: '2026-11-01' },
			],
		};
		expect(primaryMilestoneForItem(item)).toEqual({ id: 51, name: 'Beta', target_date: '2026-11-01' });
		expect(primaryLaneKeyForItem(item, 'milestone')).toBe('milestone-51');
		expect(primaryLaneKeyForItem(item, 'assignee')).toBe('assignee-7');
	});

	it('resolves the primary milestone from ids plus a reference list', () => {
		const item = { id: 1, milestone_ids: [50, 51] };
		const milestones = [
			{ id: 50, name: 'GA', target_date: '2026-12-01' },
			{ id: 51, name: 'Beta', target_date: '2026-11-01' },
		];
		expect(primaryMilestoneForItem(item, milestones)).toEqual({
			id: 51,
			name: 'Beta',
			target_date: '2026-11-01',
		});
		expect(primaryLaneKeyForItem(item, 'milestone', milestones)).toBe('milestone-51');
		expect(primaryLaneKeyForItem({ id: 2 }, 'milestone', milestones)).toBe(SWIMLANE_NONE_KEY);
	});

	it('detaches undated milestones after dated ones when choosing the primary', () => {
		const item = {
			id: 1,
			milestones: [
				{ id: 50, name: 'No date' },
				{ id: 51, name: 'Late', target_date: '2026-12-01' },
			],
		};
		expect(primaryMilestoneForItem(item)?.id).toBe(51);
	});

	it('restricts workspace-local milestones to their own workspace', () => {
		expect(milestoneAssignableToWorkspace({ id: 1, is_global: true }, 7)).toBe(true);
		expect(milestoneAssignableToWorkspace({ id: 2, is_global: false, workspace_id: 7 }, 7)).toBe(true);
		expect(milestoneAssignableToWorkspace({ id: 3, is_global: false, workspace_id: 8 }, 7)).toBe(false);
		expect(milestoneAssignableToWorkspace({ id: 4, is_global: false, workspace_id: null }, 7)).toBe(false);
		expect(milestoneAssignableToWorkspace(null, 7)).toBe(false);
	});

	it('requests confirmation only when one or more extra milestones are removed', () => {
		const milestones = [
			{ id: 50, name: 'GA', target_date: '2026-12-01' },
			{ id: 51, name: 'Beta', target_date: '2026-11-01' },
			{ id: 52, name: 'Later', target_date: '2027-01-01' },
		];
		const multi = { id: 1, milestones: [milestones[0], milestones[1]] };
		const single = { id: 2, milestones: [milestones[0]] };

		// Same primary lane: a reorder must not offer to drop the extra milestone.
		expect(milestoneDropRemovals(multi, 'milestone', 'milestone-51', milestones)).toBeNull();
		// Moving to a third lane removes both current attachments.
		expect(milestoneDropRemovals(multi, 'milestone', 'milestone-52', milestones)).toEqual([50, 51]);
		// Unassigned removes every attached milestone.
		expect(milestoneDropRemovals(multi, 'milestone', SWIMLANE_NONE_KEY, milestones)).toEqual([50, 51]);
		// A single milestone is a normal reassignment, not a multi-loss warning.
		expect(milestoneDropRemovals(single, 'milestone', 'milestone-52', milestones)).toBeNull();
		expect(milestoneDropRemovals(multi, 'assignee', 'assignee-7', milestones)).toBeNull();
	});
});

// WI-1587: the global scope loads no iteration/milestone catalogs, so lanes
// must be derived from card memberships instead of dropping assigned cards.
describe('map swimlanes — global fallback lanes', () => {
	const items = [
		{ id: 1, iteration_id: 41, milestones: [{ id: 51 }] },
		{ id: 2, iteration_id: 42, milestones: [{ id: 51 }, { id: 52 }] },
		{ id: 3, iteration_id: null, milestones: [] },
	];

	it('derives iteration lanes from memberships when no references loaded', () => {
		const lanes = buildSwimlanes({
			dimension: 'iteration',
			items,
			iterations: [],
			noneTitle: 'No iteration',
		});
		const keys = lanes.map((lane) => lane.key);
		expect(keys).toContain('iteration-41');
		expect(keys).toContain('iteration-42');
		expect(keys).toContain(SWIMLANE_NONE_KEY);
		const lane41 = lanes.find((lane) => lane.key === 'iteration-41');
		expect(lane41.count).toBe(1);
		expect(lane41.title).toBe('Iteration #41');
	});

	it('derives milestone lanes from memberships, keeping multi-lane semantics', () => {
		const lanes = buildSwimlanes({
			dimension: 'milestone',
			items,
			milestones: [],
			noneTitle: 'No milestone',
		});
		const lane51 = lanes.find((lane) => lane.key === 'milestone-51');
		const lane52 = lanes.find((lane) => lane.key === 'milestone-52');
		expect(lane51.count).toBe(2);
		expect(lane52.count).toBe(1);
		expect(lane52.title).toBe('Milestone #52');
	});

	it('prefers reference titles when the catalog is available', () => {
		const lanes = buildSwimlanes({
			dimension: 'iteration',
			items,
			iterations: [{ id: 41, name: 'Sprint 41' }],
			noneTitle: 'No iteration',
		});
		const lane41 = lanes.find((lane) => lane.key === 'iteration-41');
		expect(lane41.title).toBe('Sprint 41');
		// The uncovered reference-less lane still falls back.
		const lane42 = lanes.find((lane) => lane.key === 'iteration-42');
		expect(lane42.title).toBe('Iteration #42');
	});

	it('uses embedded milestone names for fallback titles', () => {
		const lanes = buildSwimlanes({
			dimension: 'milestone',
			items: [{ id: 1, milestones: [{ id: 60, name: 'Launch' }] }],
			milestones: [],
			noneTitle: 'No milestone',
		});
		expect(lanes.find((lane) => lane.key === 'milestone-60')?.title).toBe('Launch');
	});
});
