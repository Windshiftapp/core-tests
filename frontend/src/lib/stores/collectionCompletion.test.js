import { afterAll, beforeEach, describe, expect, it, vi } from "vitest";
import { collections } from '../api/collections.js';
import { statuses } from '../api/workflows.js';

const mocks = vi.hoisted(() => ({
	items: vi.fn(),
	backlog: vi.fn(),
	changes: vi.fn(),
	byId: vi.fn(),
}));
vi.mock("../router.js", () => ({
	currentRoute: { subscribe: () => () => {} },
	GLOBAL_COLLECTION_VIEWS: new Set(),
}));
vi.mock('../api/users.js', () => ({
	userPreferences: {
		get: vi.fn().mockResolvedValue({}),
		update: vi.fn().mockResolvedValue({}),
	},
}));
vi.mock('../api/collections.js', () => ({
  collections: { getBoardConfigurationBootstrap: async () => null },
  collectionCategories: { getAll: vi.fn().mockResolvedValue([]) },
}));
vi.mock("./workspaceDataStore.svelte.js", () => ({
	workspaceDataStore: {
		initialize: async () => {},
		initializeGlobal: async () => {},
		statuses: [
			{ id: 8, name: "Done", is_completed: false },
			{
				id: 9,
				name: "Released",
				category_name: "Archived",
				is_completed: true,
			},
		],
	},
}));
vi.mock("../features/collections/collectionService.js", () => ({
	fetchCollectionTotal: vi
		.fn()
		.mockResolvedValue({ total: 300, watermark: 100 }),
	fetchCollectionItems: mocks.items,
	fetchCollectionBacklog: mocks.backlog,
	fetchCollectionItemChanges: mocks.changes,
	fetchItemsById: mocks.byId,
	getCollection: async () => ({ name: "Saved collection" }),
}));

const { collectionStore } = await import("./collectionContext.svelte.js");
const { collectionPreferences } = await import("./collectionPreferences.svelte.js");
const result = {
	items: [{ id: 1, status_id: 8 }],
	collectionName: "Workspace",
	pagination: { page: 1, limit: 250, total_items: 1, total_pages: 1 },
	watermark: 7,
};

beforeEach(async () => {
	localStorage.clear();
	collectionPreferences.reset();
	vi.clearAllMocks();
	mocks.items.mockResolvedValue(result);
	mocks.backlog.mockResolvedValue(result);
	mocks.changes.mockResolvedValue({
		watermark: 8,
		changed_item_ids: [],
		removed_item_ids: [],
	});
	await collectionStore.load("reset", null, "workspace-list");
	vi.clearAllMocks();
});
afterAll(() => {
	localStorage.clear();
	collectionPreferences.reset();
	collectionStore.destroy();
});

describe("completed item visibility", () => {
	it("includes completed items when leaving a board for a list that shows them", async () => {
		mocks.items.mockImplementation(async (_wsId, _colId, options) => ({
			...result,
			items: options.status_id
				? [{ id: 2, status_id: 9 }]
				: options.status_id_not
					? [{ id: 1, status_id: 8 }]
					: [
							{ id: 1, status_id: 8 },
							{ id: 2, status_id: 9 },
						],
		}));
		await collectionStore.load("41", null, "workspace-board");
		localStorage.setItem("collection-show-completed:workspace-41:list", "true");
		await collectionStore.load("41", null, "workspace-list");
		expect(collectionStore.items).toEqual([
			{ id: 1, status_id: 8 },
			{ id: 2, status_id: 9 },
		]);
		expect(mocks.items).toHaveBeenLastCalledWith(
			"41",
			null,
			expect.not.objectContaining({ status_id_not: "9" }),
		);
	});

	it("ignores item data fetched by a delta poll before navigation", async () => {
		await collectionStore.load("41", null, "workspace-list");
		mocks.changes.mockResolvedValue({
			watermark: 8,
			changed_item_ids: [1],
			removed_item_ids: [],
		});
		let resolveItems;
		mocks.byId.mockImplementation(
			() =>
				new Promise((resolve) => {
					resolveItems = resolve;
				}),
		);
		const poll = collectionStore.refreshDeltas();
		await vi.waitFor(() => expect(resolveItems).toBeTypeOf("function"));
		await collectionStore.load("41", "5", "workspace-list");
		resolveItems([{ id: 1, status_id: 9 }]);
		await poll;
		expect(collectionStore.items).toEqual([{ id: 1, status_id: 8 }]);
		expect(collectionStore.itemsTotalCount).toBe(1);
	});

	it("updates the visible total when the loaded item becomes completed", async () => {
		await collectionStore.load("41", null, "workspace-list");
		collectionStore.applyItem({ id: 1, status_id: 9 });
		expect(collectionStore.items).toEqual([]);
		expect(collectionStore.itemsTotalCount).toBe(0);
	});

	it("reloads backlog data after visiting a board that included completed work", async () => {
		await collectionStore.load("41", null, "workspace-board");
		await collectionStore.setShowCompleted(true);
		await collectionStore.load("41", null, "workspace-list");
		mocks.backlog.mockClear();
		await collectionStore.load("41", null, "workspace-backlog");
		expect(mocks.backlog).toHaveBeenCalledWith(
			"41",
			null,
			expect.objectContaining({
				sub_ql: "status_completed = false",
			}),
		);
	});

	it("removes completed mutation responses while retaining an active status named Done", async () => {
		await collectionStore.load("41", null, "workspace-list");
		collectionStore.applyItem({ id: 2, status_id: 8 });
		expect(collectionStore.items).toEqual([
			{ id: 1, status_id: 8 },
			{ id: 2, status_id: 8 },
		]);
		collectionStore.applyItem({ id: 1, status_id: 9 });
		expect(collectionStore.items).toEqual([{ id: 2, status_id: 8 }]);
		collectionStore.applyItem({ id: 3, status_id: 9 });
		expect(collectionStore.items).toEqual([{ id: 2, status_id: 8 }]);
		await collectionStore.setShowCompleted(true);
		collectionStore.applyItem({ id: 1, status_id: 9 });
		expect(collectionStore.items).toEqual([{ id: 1, status_id: 9 }]);
	});

	it("keeps the toggle usable when browser storage is blocked", async () => {
		const read = vi
			.spyOn(Storage.prototype, "getItem")
			.mockImplementation(() => {
				throw new Error("blocked");
			});
		const write = vi
			.spyOn(Storage.prototype, "setItem")
			.mockImplementation(() => {
				throw new Error("blocked");
			});
		try {
			await collectionStore.load("41", null, "workspace-list");
			expect(collectionStore.showCompleted).toBe(false);
			await collectionStore.setShowCompleted(true);
			expect(mocks.items).toHaveBeenLastCalledWith(
				"41",
				null,
				expect.objectContaining({ sub_ql: undefined }),
			);
		} finally {
			read.mockRestore();
			write.mockRestore();
		}
	});

	it.each(["list", "tree", "map", "roadmap", "backlog"])(
		"filters %s by completion category before pagination",
		async (view) => {
			await collectionStore.load("41", null, `workspace-${view}`);
			const fetch = view === "backlog" ? mocks.backlog : mocks.items;
			expect(fetch).toHaveBeenLastCalledWith(
				"41",
				null,
				expect.objectContaining({
					sub_ql: "status_completed = false",
				}),
			);
		},
	);

	it.each(["workspace-board", "collection-board"])(
		"leaves completed visibility to board configuration in %s despite a saved hide preference",
		async (view) => {
			mocks.items.mockImplementation(async (_wsId, _colId, options) =>
				options.status_id
					? {
							...result,
							items: [],
							pagination: { ...result.pagination, total_items: 0 },
						}
					: result,
			);
			const colId = view === "collection-board" ? "5" : null;
			const scope = colId ? "collection-5" : "workspace-41";
			localStorage.setItem(`collection-show-completed:${scope}:board`, "false");
			await collectionStore.load("41", colId, view);
			expect(mocks.items).toHaveBeenLastCalledWith(
				"41",
				colId,
				expect.objectContaining({ sub_ql: undefined }),
			);
			collectionStore.applyItem({ id: 1, status_id: 9 });
			expect(collectionStore.items).toEqual([{ id: 1, status_id: 9 }]);
		},
	);

	it("persists the preference per view and scope, including after returning", async () => {
		await collectionStore.load("41", null, "workspace-tree");
		await collectionStore.setShowCompleted(true);
		expect(mocks.items).toHaveBeenLastCalledWith(
			"41",
			null,
			expect.objectContaining({ sub_ql: undefined }),
		);
		await collectionStore.load("41", null, "workspace-list");
		expect(collectionStore.showCompleted).toBe(false);
		expect(mocks.items).toHaveBeenLastCalledWith(
			"41",
			null,
			expect.objectContaining({ sub_ql: "status_completed = false" }),
		);
		await collectionStore.load("42", null, "workspace-tree");
		expect(collectionStore.showCompleted).toBe(false);
		await collectionStore.load("41", "5", "workspace-tree");
		expect(collectionStore.showCompleted).toBe(false);
		await collectionStore.load("41", null, "workspace-tree");
		expect(collectionStore.showCompleted).toBe(true);
		expect(mocks.items).toHaveBeenLastCalledWith(
			"41",
			null,
			expect.objectContaining({ sub_ql: undefined }),
		);
	});

	it("keeps completion filtering when sorting, refreshing, polling and clearing other filters", async () => {
		await collectionStore.load("41", null, "workspace-list");
		collectionStore.setSubFilter('priority = "High" OR priority = "Low"');
		await vi.waitFor(() => expect(collectionStore.loading).toBe(false));
		const sub_ql =
			'(priority = "High" OR priority = "Low") AND status_completed = false';
		await collectionStore.setItemsPage(2, 50);
		expect(mocks.items).toHaveBeenLastCalledWith(
			"41",
			null,
			expect.objectContaining({ sub_ql, page: 2 }),
		);
		await collectionStore.refresh();
		expect(mocks.items).toHaveBeenLastCalledWith(
			"41",
			null,
			expect.objectContaining({ sub_ql }),
		);
		await collectionStore.refreshDeltas();
		expect(mocks.changes).toHaveBeenLastCalledWith(
			"41",
			null,
			expect.objectContaining({ sub_ql }),
		);
		collectionStore.clearSubFilter();
		await vi.waitFor(() => expect(collectionStore.loading).toBe(false));
		expect(mocks.items).toHaveBeenLastCalledWith(
			"41",
			null,
			expect.objectContaining({ sub_ql: "status_completed = false" }),
		);
	});
});
