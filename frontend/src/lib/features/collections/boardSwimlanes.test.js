import { describe, expect, it } from 'vitest';
import {
	BOARD_GROUP_BY_ITEM_TYPE,
	BOARD_GROUP_BY_MILESTONE,
	MILESTONE_HIDDEN_COMPLETED_KEY,
	MILESTONE_OPEN_FROM_COMPLETED_KEY,
	boardGroupPreferenceStorageKey,
	buildBoardSwimlanes,
	legacyBoardGroupItemTypeStorageKey,
	milestonesFromItems,
	parseBoardGroupBy,
	resolveStoredBoardGroupBy,
	serializeBoardGroupBy,
} from './boardSwimlanes.js';
import { SWIMLANE_NONE_KEY } from './swimlanes.js';

const milestone = (id, name, targetDate, status = 'planning') => ({
	id,
	name,
	target_date: targetDate,
	status,
});

const item = (overrides = {}) => ({
	id: 1,
	title: 'Card',
	item_type_id: 1,
	parent_id: null,
	status_id: 10,
	milestones: [],
	...overrides,
});

describe('board swimlanes — none mode', () => {
	it('returns one lane holding every filtered card', () => {
		const items = [item({ id: 1 }), item({ id: 2 })];
		const lanes = buildBoardSwimlanes({
			groupBy: null,
			items,
			filteredItems: items,
			allItemsTitle: 'All items',
		});
		expect(lanes).toHaveLength(1);
		expect(lanes[0]).toMatchObject({
			id: 'all',
			key: null,
			dimension: null,
			title: 'All items',
			isDefault: true,
			itemCount: 2,
		});
		expect(lanes[0].items.map((entry) => entry.id)).toEqual([1, 2]);
	});
});

describe('board swimlanes — item type mode', () => {
	const parent = item({ id: 100, title: 'Epic', item_type_id: 5, status_id: 10 });
	const child = item({ id: 101, item_type_id: 6, parent_id: 100 });
	const loose = item({ id: 102, item_type_id: 6, parent_id: null });
	const otherType = item({ id: 103, item_type_id: 7, parent_id: null });

	it('groups children under their parent and appends unassigned last', () => {
		const lanes = buildBoardSwimlanes({
			groupBy: { dimension: BOARD_GROUP_BY_ITEM_TYPE, itemTypeId: 5 },
			items: [parent, child, loose, otherType],
			filteredItems: [parent, child, loose, otherType],
			unassignedTitle: 'Unassigned',
		});
		expect(lanes.map((lane) => lane.key)).toEqual(['parent-100', SWIMLANE_NONE_KEY]);
		expect(lanes[0].items.map((entry) => entry.id)).toEqual([101]);
		expect(lanes[1].items.map((entry) => entry.id)).toEqual([102, 103]);
	});

	it('moves children of a rightmost-column parent into unassigned when excluded', () => {
		const rightmostParent = item({ id: 200, title: 'Done epic', item_type_id: 5, status_id: 30 });
		const rightmostChild = item({ id: 201, item_type_id: 6, parent_id: 200 });
		const lanes = buildBoardSwimlanes({
			groupBy: { dimension: BOARD_GROUP_BY_ITEM_TYPE, itemTypeId: 5 },
			items: [rightmostParent, rightmostChild],
			filteredItems: [rightmostParent, rightmostChild],
			statusIdForItem: (entry) => entry.status_id,
			rightmostStatusIds: new Set([30]),
			excludeRightmostSwimlaneParents: true,
			unassignedTitle: 'Unassigned',
		});
		expect(lanes.map((lane) => lane.key)).toEqual([SWIMLANE_NONE_KEY]);
		expect(lanes[0].items.map((entry) => entry.id)).toEqual([201]);
	});

	it('keeps a parent lane when the parent card matches the filter even without children', () => {
		const lanes = buildBoardSwimlanes({
			groupBy: { dimension: BOARD_GROUP_BY_ITEM_TYPE, itemTypeId: 5 },
			items: [parent],
			filteredItems: [parent],
			unassignedTitle: 'Unassigned',
		});
		expect(lanes[0].key).toBe('parent-100');
		expect(lanes[0].itemCount).toBe(0);
		expect(lanes[0].parentIsVisible).toBe(true);
	});
});

describe('board swimlanes — milestone mode', () => {
	const ga = milestone(50, 'GA', '2026-12-01');
	const beta = milestone(51, 'Beta', '2026-11-01');
	const undatedActive = milestone(52, 'Backlog release', null);
	const completed = milestone(53, 'Old release', '2026-01-01', 'completed');

	it('orders lanes by target date and always appends unassigned last', () => {
		const items = [
			item({ id: 1, milestones: [ga] }),
			item({ id: 2, milestones: [beta] }),
			item({ id: 3, milestones: [] }),
			item({ id: 4, milestones: [undatedActive] }),
		];
		const lanes = buildBoardSwimlanes({
			groupBy: { dimension: BOARD_GROUP_BY_MILESTONE },
			items,
			filteredItems: items,
			milestones: [ga, beta, undatedActive],
			unassignedTitle: 'Unassigned',
		});
		expect(lanes.map((lane) => lane.key)).toEqual([
			'milestone-51',
			'milestone-50',
			'milestone-52',
			SWIMLANE_NONE_KEY,
		]);
		expect(lanes.at(-1)).toMatchObject({ id: 'unassigned', key: SWIMLANE_NONE_KEY });
		expect(lanes.at(-1).items.map((entry) => entry.id)).toEqual([3]);
	});

	it('shows empty active milestones but hides empty completed ones', () => {
		const items = [item({ id: 1, milestones: [ga] })];
		const lanes = buildBoardSwimlanes({
			groupBy: { dimension: BOARD_GROUP_BY_MILESTONE },
			items,
			filteredItems: items,
			milestones: [ga, beta, completed],
			includeEmptyMilestones: true,
			unassignedTitle: 'Unassigned',
		});
		expect(lanes.map((lane) => lane.key)).toContain('milestone-51');
		expect(lanes.map((lane) => lane.key)).not.toContain('milestone-53');
	});

	it('keeps a completed milestone lane while it still has cards', () => {
		const items = [item({ id: 1, milestones: [completed] })];
		const lanes = buildBoardSwimlanes({
			groupBy: { dimension: BOARD_GROUP_BY_MILESTONE },
			items,
			filteredItems: items,
			milestones: [ga, completed],
			includeEmptyMilestones: true,
			unassignedTitle: 'Unassigned',
		});
		expect(lanes.map((lane) => lane.key)).toContain('milestone-53');
	});

	it('places a multi-milestone card in its earliest-target-date lane only', () => {
		const items = [item({ id: 1, milestones: [ga, beta] })];
		const lanes = buildBoardSwimlanes({
			groupBy: { dimension: BOARD_GROUP_BY_MILESTONE },
			items,
			filteredItems: items,
			milestones: [ga, beta],
			unassignedTitle: 'Unassigned',
		});
		const betaLane = lanes.find((lane) => lane.key === 'milestone-51');
		const gaLane = lanes.find((lane) => lane.key === 'milestone-50');
		expect(betaLane.items.map((entry) => entry.id)).toEqual([1]);
		expect(gaLane.items).toEqual([]);
	});

	it('creates a fallback lane for a milestone missing from the catalog', () => {
		const stale = { id: 99, name: 'Stale release', target_date: '2026-10-01' };
		const items = [item({ id: 1, milestones: [stale] }), item({ id: 2 })];
		const lanes = buildBoardSwimlanes({
			groupBy: { dimension: BOARD_GROUP_BY_MILESTONE },
			items,
			filteredItems: items,
			milestones: [ga],
			includeEmptyMilestones: false,
			unassignedTitle: 'Unassigned',
		});
		expect(lanes.map((lane) => lane.key)).toEqual(['milestone-99', SWIMLANE_NONE_KEY]);
		expect(lanes[0].title).toBe('Stale release');
	});

	it('hides empty global milestones unless the board pins them', () => {
		const local = milestone(60, 'Local', '2026-12-01');
		const globalEmpty = { ...milestone(61, 'Global empty', '2026-11-01'), is_global: true };
		const globalPinned = { ...milestone(62, 'Global pinned', '2026-10-01'), is_global: true };
		const globalWithCard = { ...milestone(63, 'Global with card', '2026-09-01'), is_global: true };
		const items = [item({ id: 1, milestones: [globalWithCard] })];
		const lanes = buildBoardSwimlanes({
			groupBy: { dimension: BOARD_GROUP_BY_MILESTONE },
			items,
			filteredItems: items,
			milestones: [local, globalEmpty, globalPinned, globalWithCard],
			includeEmptyMilestones: true,
			globalMilestoneIds: [62],
			unassignedTitle: 'Unassigned',
		});
		const keys = lanes.map((lane) => lane.key);
		expect(keys).toContain('milestone-60');
		expect(keys).toContain('milestone-62');
		expect(keys).toContain('milestone-63');
		expect(keys).not.toContain('milestone-61');
	});

	it('aggregates cards from hidden completed milestones before unassigned', () => {
		const active = milestone(70, 'Active', '2026-12-01');
		const completed = milestone(71, 'Shipped', '2026-01-01', 'completed');
		const items = [item({ id: 1, milestones: [completed] }), item({ id: 2, milestones: [] })];
		const lanes = buildBoardSwimlanes({
			groupBy: { dimension: BOARD_GROUP_BY_MILESTONE },
			items,
			filteredItems: items,
			milestones: [active, completed],
			includeEmptyMilestones: true,
			showCompletedMilestones: false,
			hiddenCompletedTitle: 'Completed milestones',
			unassignedTitle: 'Unassigned',
		});
		expect(lanes.map((lane) => lane.key)).toEqual([
			'milestone-70',
			MILESTONE_HIDDEN_COMPLETED_KEY,
			SWIMLANE_NONE_KEY,
		]);
		const aggregate = lanes.find((lane) => lane.key === MILESTONE_HIDDEN_COMPLETED_KEY);
		expect(aggregate).toMatchObject({
			title: 'Completed milestones',
			isAggregate: true,
			milestone: null,
		});
		expect(aggregate.items.map((entry) => entry.id)).toEqual([1]);
	});

	it('keeps completed milestone lanes when showCompletedMilestones is on', () => {
		const completed = milestone(72, 'Shipped', '2026-01-01', 'completed');
		const items = [item({ id: 1, milestones: [completed] })];
		const lanes = buildBoardSwimlanes({
			groupBy: { dimension: BOARD_GROUP_BY_MILESTONE },
			items,
			filteredItems: items,
			milestones: [completed],
			showCompletedMilestones: true,
			unassignedTitle: 'Unassigned',
		});
		expect(lanes.map((lane) => lane.key)).toContain('milestone-72');
		expect(lanes.map((lane) => lane.key)).not.toContain(MILESTONE_HIDDEN_COMPLETED_KEY);
	});
});

describe('board swimlanes — open-from-completed rescue lane', () => {
	const active = milestone(80, 'Active', '2026-12-01');
	const completed = milestone(81, 'Shipped', '2026-01-01', 'completed');
	const isItemOpen = (entry) => entry.open !== false;

	it('collects open cards from completed milestones into a leading rescue lane', () => {
		const items = [
			item({ id: 1, milestones: [completed], open: true }),
			item({ id: 2, milestones: [completed], open: false }),
			item({ id: 3, milestones: [active], open: true }),
		];
		const lanes = buildBoardSwimlanes({
			groupBy: { dimension: BOARD_GROUP_BY_MILESTONE },
			items,
			filteredItems: items,
			milestones: [active, completed],
			showOpenFromCompletedMilestones: true,
			isItemOpen,
			openFromCompletedTitle: 'Open from completed milestones',
			unassignedTitle: 'Unassigned',
		});
		expect(lanes.map((lane) => lane.key)).toEqual([
			MILESTONE_OPEN_FROM_COMPLETED_KEY,
			'milestone-81',
			'milestone-80',
			SWIMLANE_NONE_KEY,
		]);
		const rescue = lanes[0];
		expect(rescue).toMatchObject({
			title: 'Open from completed milestones',
			isRescue: true,
			milestone: null,
		});
		expect(rescue.items.map((entry) => entry.id)).toEqual([1]);
		const completedLane = lanes.find((lane) => lane.key === 'milestone-81');
		expect(completedLane.items.map((entry) => entry.id)).toEqual([2]);
	});

	it('splits open and closed cards between the rescue lane and the hidden aggregate', () => {
		const items = [
			item({ id: 1, milestones: [completed], open: true }),
			item({ id: 2, milestones: [completed], open: false }),
		];
		const lanes = buildBoardSwimlanes({
			groupBy: { dimension: BOARD_GROUP_BY_MILESTONE },
			items,
			filteredItems: items,
			milestones: [active, completed],
			showCompletedMilestones: false,
			showOpenFromCompletedMilestones: true,
			isItemOpen,
			unassignedTitle: 'Unassigned',
		});
		expect(lanes.map((lane) => lane.key)).toEqual([
			MILESTONE_OPEN_FROM_COMPLETED_KEY,
			'milestone-80',
			MILESTONE_HIDDEN_COMPLETED_KEY,
			SWIMLANE_NONE_KEY,
		]);
		expect(lanes[0].items.map((entry) => entry.id)).toEqual([1]);
		const aggregate = lanes.find((lane) => lane.key === MILESTONE_HIDDEN_COMPLETED_KEY);
		expect(aggregate.items.map((entry) => entry.id)).toEqual([2]);
	});

	it('omits the rescue lane when the toggle is off or nothing is open', () => {
		const items = [item({ id: 1, milestones: [completed], open: false })];
		const off = buildBoardSwimlanes({
			groupBy: { dimension: BOARD_GROUP_BY_MILESTONE },
			items,
			filteredItems: items,
			milestones: [completed],
			showOpenFromCompletedMilestones: false,
			isItemOpen,
			unassignedTitle: 'Unassigned',
		});
		expect(off.map((lane) => lane.key)).not.toContain(MILESTONE_OPEN_FROM_COMPLETED_KEY);

		const noOpen = buildBoardSwimlanes({
			groupBy: { dimension: BOARD_GROUP_BY_MILESTONE },
			items,
			filteredItems: items,
			milestones: [completed],
			showOpenFromCompletedMilestones: true,
			isItemOpen,
			unassignedTitle: 'Unassigned',
		});
		expect(noOpen.map((lane) => lane.key)).not.toContain(MILESTONE_OPEN_FROM_COMPLETED_KEY);
	});
});

describe('board swimlanes — group-by preference', () => {
	it('serializes and parses milestone and item-type selections', () => {
		expect(serializeBoardGroupBy({ dimension: BOARD_GROUP_BY_MILESTONE })).toBe('milestone');
		expect(serializeBoardGroupBy({ dimension: BOARD_GROUP_BY_ITEM_TYPE, itemTypeId: 7 })).toBe('type:7');
		expect(serializeBoardGroupBy(null)).toBeNull();
		expect(parseBoardGroupBy('milestone')).toEqual({ dimension: BOARD_GROUP_BY_MILESTONE });
		expect(parseBoardGroupBy('type:7')).toEqual({ dimension: BOARD_GROUP_BY_ITEM_TYPE, itemTypeId: 7 });
		expect(parseBoardGroupBy('garbage')).toBeNull();
	});

	it('falls back to the legacy item-type storage key', () => {
		const itemTypes = [{ id: 5 }, { id: 7 }];
		expect(resolveStoredBoardGroupBy(null, '7', itemTypes)).toEqual({
			dimension: BOARD_GROUP_BY_ITEM_TYPE,
			itemTypeId: 7,
		});
		expect(resolveStoredBoardGroupBy('milestone', '7', itemTypes)).toEqual({
			dimension: BOARD_GROUP_BY_MILESTONE,
		});
		expect(resolveStoredBoardGroupBy(null, '9', itemTypes)).toBeNull();
		expect(resolveStoredBoardGroupBy('type:9', null, itemTypes)).toBeNull();
	});

	it('stores preferences per collection or workspace scope', () => {
		expect(boardGroupPreferenceStorageKey('collection-9')).toBe('board-group-by-collection-9');
		expect(legacyBoardGroupItemTypeStorageKey('workspace-3')).toBe(
			'board-group-by-item-type-workspace-3'
		);
	});
});

describe('board swimlanes — milestone reference from cards', () => {
	it('dedupes and orders embedded milestones for global boards', () => {
		const items = [
			item({ id: 1, milestones: [{ id: 50, name: 'GA', target_date: '2026-12-01' }] }),
			item({ id: 2, milestones: [{ id: 50, name: 'GA' }, { id: 51, name: 'Beta', target_date: '2026-11-01' }] }),
		];
		expect(milestonesFromItems(items).map((entry) => entry.id)).toEqual([51, 50]);
	});
});
