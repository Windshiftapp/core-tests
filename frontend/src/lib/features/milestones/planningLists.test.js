import { cleanup, render, screen, waitFor } from "@testing-library/svelte";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ getAll: vi.fn() }));

vi.mock('../../api/milestones.js', () => ({
  milestones: { getAll: mocks.getAll },
  iterations: { getAll: mocks.getAll },
  milestoneCategories: { getAll: async () => [] },
  iterationTypes: { getAll: async () => [] },
}))

vi.mock('../../api/workspaces.js', () => ({
  workspaces: { getAll: async () => [] },
}))

vi.mock('../../api/admin.js', () => ({
  setup: {
  			getModuleSettings: async () => ({ test_management_enabled: false }),
  		},
}));
vi.mock("../../stores/permissions.svelte.js", () => ({
	permissionStore: {
		subscribe: (run) => {
			run({ userPermissionKeys: new Set() });
			return () => {};
		},
	},
	isSystemAdmin: {
		subscribe: (run) => {
			run(false);
			return () => {};
		},
	},
}));
vi.mock("../../stores/workspacePermissions.svelte.js", () => ({
	workspacePermissions: {
		canAdminWorkspace: () => false,
		hasPermission: () => false,
	},
}));
vi.mock("../../router.js", () => ({
	currentRoute: {
		subscribe: (run) => {
			run({ params: {} });
			return () => {};
		},
	},
	navigate: vi.fn(),
}));
vi.mock("../../stores/i18n.svelte.js", () => ({ t: (key) => key }));

import { milestonesStore } from "../../stores/milestones.js";
import Iterations from "../iterations/Iterations.svelte";
import Milestones from "./Milestones.svelte";
import { iterationTypes, iterations, milestoneCategories, milestones } from '../../api/milestones.js';
import { workspaces } from '../../api/workspaces.js';
import { setup } from '../../api/admin.js';



beforeEach(() => {
	milestonesStore.reset();
	mocks.getAll.mockReset();
	mocks.getAll.mockImplementation(async (filters) => {
		const global = {
			id: 1,
			name: "Shared release",
			is_global: true,
			status: "planning",
		};
		const local = {
			id: 2,
			name: "Workspace release",
			is_global: false,
			workspace_id: 23,
			status: "planning",
		};
		return filters?.is_global === true ? [global] : [global, local];
	});
});
afterEach(cleanup);

describe.each([
	["milestones", Milestones],
	["iterations", Iterations],
])("%s list scope", (_name, Component) => {
	it("shows only global entries on the global list", async () => {
		render(Component);
		await screen.findByText("Shared release");
		expect(screen.queryByText("Workspace release")).not.toBeInTheDocument();
		expect(mocks.getAll).toHaveBeenCalledWith({ is_global: true });
	});

	it("includes local and global entries in a workspace", async () => {
		render(Component, { workspaceId: 23 });
		await screen.findByText("Workspace release");
		expect(screen.getByText("Shared release")).toBeInTheDocument();
		await waitFor(() =>
			expect(mocks.getAll).toHaveBeenCalledWith({
				workspace_id: 23,
				include_global: true,
			}),
		);
	});
});
