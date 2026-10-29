import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { beforeEach, describe, expect, it, vi } from "vitest";

import WorkspaceAgentSkills from "./WorkspaceAgentSkills.svelte";
import { agentSkills } from '../api/agentSkills.js';

vi.mock('../api/agentSkills.js', () => ({
  agentSkills: {
  		listForWorkspace: vi.fn(),
  		create: vi.fn(),
  		update: vi.fn(),
  		remove: vi.fn(),
  	},
}));

describe("WorkspaceAgentSkills snapshot editor", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		agentSkills.listForWorkspace.mockResolvedValue([
			{
				id: 4,
				name: "release-notes",
				description: "How to publish",
				body: "# Release notes",
				enabled: true,
				pages: [
					{
						id: 9,
						title: "Current runbook",
						snapshot_title: "Saved runbook",
						stale: true,
					},
				],
				usage: {
					bytes: 2048,
					estimated_tokens: 600,
					max_bytes: 262144,
					max_tokens: 65536,
				},
			},
		]);
	});

	it("explains saved page disclosure and flags stale snapshots", async () => {
		render(WorkspaceAgentSkills, { props: { workspaceId: 1 } });
		await waitFor(() =>
			expect(agentSkills.listForWorkspace).toHaveBeenCalledWith(1),
		);
		await fireEvent.click(await screen.findByTestId("agent-skill-edit"));

		expect(
			await screen.findByTestId("agent-skill-page-stale"),
		).toHaveTextContent("Updated since snapshot");
		expect(
			screen.getByText(/regardless of the page's own viewer list/i),
		).toBeInTheDocument();
		expect(screen.getByTestId("agent-skill-usage")).toHaveTextContent(
			"256 KiB",
		);
	});

	it("blocks saving when the estimated activation exceeds its ceiling", async () => {
		render(WorkspaceAgentSkills, { props: { workspaceId: 1 } });
		await fireEvent.click(await screen.findByTestId("agent-skill-edit"));
		await screen.findByTestId("agent-skill-editor");
		await fireEvent.input(document.querySelector("#agent-skill-body"), {
			target: { value: "x".repeat(262145) },
		});

		expect(screen.getByTestId("agent-skill-save")).toBeDisabled();
		expect(
			screen.getByText("Remove content or references before saving."),
		).toBeInTheDocument();
	});
});
