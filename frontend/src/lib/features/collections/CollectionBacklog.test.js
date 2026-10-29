import { cleanup, render, screen } from "@testing-library/svelte";
import { afterEach, describe, expect, it, vi } from "vitest";

const { store, workspaceData, defaultBacklogItems } = vi.hoisted(() => ({
	defaultBacklogItems: [
		{ id: 1, title: "First item" },
		{ id: 2, title: "Second item" },
	],
	store: {
		backlogItems: [
			{ id: 1, title: "First item" },
			{ id: 2, title: "Second item" },
		],
		backlogPagination: { total_items: 293 },
		collectionTotal: 1299,
		backlogHasMore: true,
		loading: false,
		subFilterQL: "",
		effectiveSubFilterQL: "",
		loadMoreBacklog: vi.fn(),
		insertCreatedItem: vi.fn(),
	},
	workspaceData: {
		workspace: { id: 1, name: "Workspace" },
		itemTypes: [],
		statuses: [],
		statusCategories: [],
		screenConfigs: {},
		initialize: vi.fn(),
		initializeGlobal: vi.fn(),
		screenConfigFor: vi.fn().mockResolvedValue(null),
	},
}));

vi.mock("../../stores/collectionContext.js", () => ({
	collectionStore: store,
	scheduleCollectionReconciliation: vi.fn(),
	reloadCollection: vi.fn(),
}));
vi.mock('../../stores/backlogStore.svelte.js', () => ({
  backlogStore: { setCount: vi.fn() },
}))

vi.mock('../../stores/workspaceDataStore.svelte.js', () => ({
  workspaceDataStore: workspaceData,
}));
vi.mock('../../api/configuration.js', () => ({
  screens: { getAllWithFields: vi.fn().mockResolvedValue([]) },
}))

vi.mock('../../api/milestones.js', () => ({
  iterations: { getAll: vi.fn().mockResolvedValue([]) },
  milestoneCategories: { getAll: vi.fn().mockResolvedValue([]) },
}));
vi.mock("../../stores/workspaceGradient.svelte.js", () => ({
	useGradientStyles: () => ({}),
	loadWorkspaceGradient: vi.fn(),
}));
vi.mock("../../stores/i18n.svelte.js", () => ({
	t: (key, params = {}) =>
		({
			"layout.items": "items",
			"collections.itemsShown": `${params.count} shown`,
			"collections.showingItemsFromBacklog": `Showing ${params.count} items from backlog`,
			"common.loadMore": "Load more",
			"common.remaining": "remaining",
		})[key] ?? key,
}));
vi.mock(
	"./BacklogIterationSection.svelte",
	() => import("./EmptyBacklogChild.svelte"),
);
vi.mock("./SubFilterBar.svelte", () => import("./EmptyBacklogChild.svelte"));
vi.mock(
	"./CollectionViewSwitcher.svelte",
	() => import("./EmptyBacklogChild.svelte"),
);
vi.mock(
	"../../pickers/ItemPicker.svelte",
	() => import("./EmptyBacklogChild.svelte"),
);
vi.mock(
	"../items/ItemDetail.svelte",
	() => import("./EmptyBacklogChild.svelte"),
);
vi.mock(
	"../../dialogs/CompleteIterationDialog.svelte",
	() => import("./EmptyBacklogChild.svelte"),
);

import CollectionBacklog from "./CollectionBacklog.svelte";
import { screens } from '../../api/configuration.js';
import { iterations } from '../../api/milestones.js';


afterEach(() => {
	cleanup();
	localStorage.clear();
	store.backlogItems = [...defaultBacklogItems];
	store.backlogPagination = { total_items: 293 };
	store.backlogHasMore = true;
	workspaceData.screenConfigFor.mockClear();
});

describe("backlog creation events", () => {
	it("routes a created item through the store instead of writing the array", async () => {
		store.insertCreatedItem.mockClear();
		render(CollectionBacklog, { workspaceId: 1 });

		window.dispatchEvent(
			new CustomEvent("refresh-work-items", {
				detail: { itemId: 55, item: { id: 55, workspace_id: 1 } },
			}),
		);

		await vi.waitFor(() =>
			expect(store.insertCreatedItem).toHaveBeenCalledWith(
				{ id: 55, workspace_id: 1 },
				{ backlog: true },
			),
		);
	});
});

describe("backlog field availability", () => {
	it("requests effective config once per distinct workspace and item type", async () => {
		store.backlogItems = [
			{ id: 1, title: "A", workspace_id: 1, item_type_id: 5 },
			{ id: 2, title: "B", workspace_id: 1, item_type_id: 5 },
			{ id: 3, title: "C", workspace_id: 1, item_type_id: 6 },
			{ id: 4, title: "D", workspace_id: 2, item_type_id: 5 },
		];
		render(CollectionBacklog, { workspaceId: 1 });

		await vi.waitFor(() =>
			expect(workspaceData.screenConfigFor).toHaveBeenCalledTimes(3),
		);
		expect(workspaceData.screenConfigFor).toHaveBeenCalledWith(1, 5);
		expect(workspaceData.screenConfigFor).toHaveBeenCalledWith(1, 6);
		expect(workspaceData.screenConfigFor).toHaveBeenCalledWith(2, 5);
		expect(screens.getAllWithFields).not.toHaveBeenCalled();
	});

	it("does not request metadata for items without a workspace", async () => {
		store.backlogItems = [{ id: 1, title: "A" }];
		render(CollectionBacklog, { workspaceId: 1 });

		await Promise.resolve();
		expect(workspaceData.screenConfigFor).not.toHaveBeenCalled();
	});
});

describe("backlog counts", () => {
	it.each([
		[293, [], "293 items · 2 shown", "291 remaining"],
		[2, [], "2 items", null],
		[293, ["unassigned"], "293 items · 0 shown", "291 remaining"],
		[null, [], "2 items", null],
	])(
		"uses backlog totals and visible rows with total %s and collapsed %j",
		async (total, collapsed, summary, remaining) => {
			store.backlogPagination = total === null ? null : { total_items: total };
			store.backlogHasMore = total > store.backlogItems.length;
			localStorage.setItem(
				"backlog-collapsed-sections-1",
				JSON.stringify(collapsed),
			);
			render(CollectionBacklog, { workspaceId: 1 });

			expect(
				await screen.findByTestId("page-header-subtitle"),
			).toHaveTextContent(`Workspace • ${summary}`);
			expect(screen.getByTestId("backlog-count-summary")).toHaveTextContent(
				summary,
			);
			if (remaining) {
				expect(
					screen.getByText(`Load more (${remaining})`),
				).toBeInTheDocument();
			} else {
				expect(screen.queryByText(/Load more/)).not.toBeInTheDocument();
			}
		},
	);
});
