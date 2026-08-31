import { cleanup, render, screen, waitFor } from "@testing-library/svelte";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const permissionState = vi.hoisted(() => ({ admin: false }));

vi.mock("../../api.js", () => ({
	agentBindings: {
		listCatalog: vi.fn(),
	},
	agentRuns: {
		listForWorkspace: vi.fn(),
	},
}));

vi.mock("../../stores", () => ({
	workspacePermissions: {
		canAdminWorkspace: vi.fn(() => permissionState.admin),
	},
}));

vi.mock("../../stores/i18n.svelte.js", () => ({
	t: (key, params = {}) => {
		const messages = {
			"workspaceAgents.availability.offline": "Offline",
			"workspaceAgents.catalog.codingInteraction": "queues work",
			"workspaceAgents.catalog.lastRun": `Last run ${params.status}`,
			"workspaceAgents.catalog.ownedBy": `Owned by ${params.name}`,
			"workspaceAgents.runStatuses.succeeded": "succeeded",
		};
		return messages[key] ?? key;
	},
}));

import { agentBindings, agentRuns } from "../../api.js";
import AgentCatalog from "./AgentCatalog.svelte";

const readyAgent = {
	id: 42,
	workspace_id: 7,
	name: "Workspace Guide",
	handle: "workspace-guide",
	purpose: "Answers questions about this workspace.",
	profile_type: "standard",
	runtime: "windshift",
	identity_class: "workspace_managed",
	lifecycle: "ready",
	availability: "ready",
	available: true,
	profile_version: 3,
	model_summary: "gpt-example",
};

beforeEach(() => {
	permissionState.admin = false;
	agentBindings.listCatalog.mockResolvedValue([readyAgent]);
	agentRuns.listForWorkspace.mockResolvedValue([
		{
			id: 5,
			binding_id: 42,
			status: "succeeded",
			updated_at: "2026-07-29T12:00:00Z",
		},
	]);
});

afterEach(() => {
	cleanup();
	vi.clearAllMocks();
});

describe("AgentCatalog", () => {
	it("renders the member-safe catalog and links cards to unified profiles", async () => {
		render(AgentCatalog, { props: { workspaceId: 7 } });

		const card = await screen.findByTestId("agent-catalog-card");
		expect(agentBindings.listCatalog).toHaveBeenCalledWith(7);
		expect(card).toHaveAttribute("href", "/workspaces/7/agents/42");
		expect(screen.getByText("Workspace Guide")).toBeInTheDocument();
		expect(screen.getByText("gpt-example")).toBeInTheDocument();
		expect(screen.getByText(/Last run succeeded/)).toBeInTheDocument();
		expect(
			screen.queryByTestId("agent-catalog-manage"),
		).not.toBeInTheDocument();
	});

	it("exposes creation to workspace administrators without linking the settings editor", async () => {
		permissionState.admin = true;

		render(AgentCatalog, { props: { workspaceId: 7 } });

		await waitFor(() =>
			expect(screen.getByTestId("agent-catalog-manage")).toBeInTheDocument(),
		);
		expect(screen.getByTestId("agent-catalog-manage")).toHaveAttribute(
			"href",
			"/workspaces/7/agents/new",
		);
		expect(screen.getByTestId("agent-catalog-manage")).toHaveTextContent("A");
		expect(
			document.querySelector('a[href*="settings/coding-agents"]'),
		).toBeNull();
	});

	it("shows server-authorized owner attribution for a user-owned identity", async () => {
		agentBindings.listCatalog.mockResolvedValue([
			{
				...readyAgent,
				identity_class: "user_owned",
				owner_name: "Ada Owner",
			},
		]);

		render(AgentCatalog, { props: { workspaceId: 7 } });

		expect(await screen.findByText(/Owned by Ada Owner/)).toBeInTheDocument();
	});

	it("labels an offline Coding agent as queueable instead of unavailable", async () => {
		agentBindings.listCatalog.mockResolvedValue([
			{
				...readyAgent,
				profile_type: "coding",
				runtime: "authorized_runner",
				availability: "offline",
				available: true,
			},
		]);

		render(AgentCatalog, { props: { workspaceId: 7 } });

		expect(await screen.findByText("Offline")).toBeInTheDocument();
		expect(screen.getByText("queues work")).toBeInTheDocument();
	});
});
