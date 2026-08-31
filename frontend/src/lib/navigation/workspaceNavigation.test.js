import { describe, expect, test } from "vitest";
import {
	workspaceOnlyViews,
	workspaceSettingsItems,
	workspaceViewItems,
} from "./workspaceNavigation.js";

describe("Agent Studio workspace navigation", () => {
	test("keeps Agents in the workspace tools group", () => {
		expect(workspaceViewItems.some((item) => item.id === "agents")).toBe(false);
		expect(workspaceOnlyViews[0]).toEqual(
			expect.objectContaining({
				id: "agents",
				labelKey: "users.agents.title",
				testId: "workspace-nav-agents",
				activeViews: [
					"workspace-agents",
					"workspace-agent-profile",
					"workspace-agent-create",
				],
			}),
		);
		expect(
			workspaceSettingsItems.some((item) => item.id === "coding-agents"),
		).toBe(false);
	});
});
