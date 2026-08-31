import {
	cleanup,
	fireEvent,
	render,
	screen,
	waitFor,
} from "@testing-library/svelte";
import {
	afterEach,
	beforeAll,
	beforeEach,
	describe,
	expect,
	test,
	vi,
} from "vitest";

vi.mock("../../api.js", () => ({
	api: {
		items: {
			update: vi.fn(async (id, values) => ({ id, ...values })),
			transition: vi.fn(async (id, statusId) => ({ id, status_id: statusId })),
		},
		workspaces: {
			getStatuses: vi.fn(),
			getProjects: vi.fn(),
			get: vi.fn(),
		},
		getAssignableUsers: vi.fn(),
		milestones: { getAll: vi.fn() },
		iterations: { getAll: vi.fn() },
		priorities: { getAll: vi.fn() },
		portalCustomers: { getAll: vi.fn() },
		customerOrganisations: { getAll: vi.fn() },
		personalLabels: { getAll: vi.fn() },
		assets: { getAll: vi.fn() },
		links: { getForItems: vi.fn(async () => ({})) },
	},
}));

vi.mock("../../stores/i18n.svelte.js", () => ({
	t: (key) => key,
	i18n: { locale: "en-US" },
}));

beforeAll(() => {
	if (!Element.prototype.animate) {
		Element.prototype.animate = () => ({
			finished: Promise.resolve(),
			cancel: () => {},
			addEventListener: () => {},
			removeEventListener: () => {},
		});
	}
	if (!Element.prototype.scrollIntoView)
		Element.prototype.scrollIntoView = () => {};
	if (!globalThis.ResizeObserver) {
		globalThis.ResizeObserver = class {
			observe() {}
			unobserve() {}
			disconnect() {}
		};
	}
	if (!globalThis.requestAnimationFrame) {
		globalThis.requestAnimationFrame = (callback) => {
			callback(performance.now());
			return 0;
		};
	}
});

const { api } = await import("../../api.js");
const { collectionEditorOptions } = await import(
	"../../stores/collectionEditorOptions.svelte.js"
);
const { collectionFieldLinks } = await import(
	"../../stores/collectionFieldLinks.svelte.js"
);
const { default: ListCellRenderer } = await import("./ListCellRenderer.svelte");

const assigneeColumn = { field_type: "system", field_identifier: "assignee" };

function renderAssignee(itemId, workspaceId) {
	return render(ListCellRenderer, {
		props: {
			item: {
				id: itemId,
				workspace_id: workspaceId,
				title: `Item ${itemId}`,
				assignee_id: null,
				custom_field_values: {},
			},
			column: assigneeColumn,
			workspace: { id: workspaceId },
			canEdit: true,
		},
	});
}

async function openAssignee(itemId) {
	await fireEvent.click(screen.getByTestId(`list-cell-assignee-${itemId}`));
}

describe("ListCellRenderer collection option loading (WI-630)", () => {
	beforeEach(() => {
		collectionEditorOptions.reset();
		collectionFieldLinks.reset();
	});

	afterEach(() => {
		cleanup();
		document.body.innerHTML = "";
	});

	test("loads nothing until an editable cell is opened and single-flights same-workspace rows", async () => {
		let resolveUsers;
		api.getAssignableUsers.mockReturnValue(
			new Promise((resolve) => {
				resolveUsers = resolve;
			}),
		);

		renderAssignee(101, 11);
		renderAssignee(102, 11);

		expect(api.getAssignableUsers).not.toHaveBeenCalled();

		await openAssignee(101);
		await openAssignee(102);

		await waitFor(() =>
			expect(api.getAssignableUsers).toHaveBeenCalledTimes(1),
		);
		expect(api.getAssignableUsers).toHaveBeenCalledWith(11);

		resolveUsers([{ id: 1101, first_name: "Eleven", last_name: "User" }]);
		await waitFor(() => {
			expect(screen.getAllByTestId("user-picker-option-1101")).toHaveLength(2);
		});
	});

	test("uses the owning workspace for each row in a mixed-workspace collection", async () => {
		api.getAssignableUsers.mockImplementation(async (workspaceId) => [
			{
				id: workspaceId * 100,
				first_name: `Workspace ${workspaceId}`,
				last_name: "User",
			},
		]);

		renderAssignee(201, 11);
		renderAssignee(202, 22);

		await openAssignee(201);
		await waitFor(() =>
			expect(screen.getByTestId("user-picker-option-1100")).toBeInTheDocument(),
		);
		await fireEvent.click(screen.getByTestId("user-picker-option-1100"));

		await openAssignee(202);
		await waitFor(() =>
			expect(screen.getByTestId("user-picker-option-2200")).toBeInTheDocument(),
		);

		expect(api.getAssignableUsers).toHaveBeenCalledTimes(2);
		expect(api.getAssignableUsers).toHaveBeenNthCalledWith(1, 11);
		expect(api.getAssignableUsers).toHaveBeenNthCalledWith(2, 22);
		expect(collectionEditorOptions.get(11).users[0].id).toBe(1100);
		expect(collectionEditorOptions.get(22).users[0].id).toBe(2200);
	});

	test("reuses cached options after a row is unmounted and rendered again", async () => {
		api.getAssignableUsers.mockResolvedValue([
			{ id: 3300, first_name: "Cached", last_name: "User" },
		]);

		const firstPage = renderAssignee(301, 33);
		await openAssignee(301);
		await waitFor(() =>
			expect(screen.getByTestId("user-picker-option-3300")).toBeInTheDocument(),
		);
		firstPage.unmount();
		document.body.innerHTML = "";

		renderAssignee(302, 33);
		await openAssignee(302);
		await waitFor(() =>
			expect(screen.getByTestId("user-picker-option-3300")).toBeInTheDocument(),
		);

		expect(api.getAssignableUsers).toHaveBeenCalledTimes(1);
	});
});
