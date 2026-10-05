import { describe, expect, it, vi } from "vitest";
import {
	agentOwnerName,
	historyTelemetryRunIDs,
	isAIChatAttributed,
	loadAttributedComments,
	loadAttributedItemHistory,
	MAX_HISTORY_TELEMETRY_RUNS,
} from "./activityAttributionData.js";

describe("item activity attribution request graph", () => {
	it("loads attributed comments without per-agent owner requests", async () => {
		const apiClient = {
			getComments: vi.fn().mockResolvedValue({
				comments: [{ id: 1, agent_owner_name: "Agent Owner" }],
			}),
			getAgentOwner: vi.fn(),
		};

		const response = await loadAttributedComments(apiClient, 42, { limit: 25 });

		expect(apiClient.getComments).toHaveBeenCalledWith(42, { limit: 25 });
		expect(apiClient.getAgentOwner).not.toHaveBeenCalled();
		expect(agentOwnerName(response.comments[0])).toBe("Agent Owner");
	});

	it("loads attributed history without per-agent owner requests", async () => {
		const apiClient = {
			items: {
				getHistory: vi
					.fn()
					.mockResolvedValue([{ id: 2, agent_owner_name: "History Owner" }]),
			},
			getAgentOwner: vi.fn(),
		};

		const history = await loadAttributedItemHistory(apiClient, 42);

		expect(apiClient.items.getHistory).toHaveBeenCalledWith(42);
		expect(apiClient.getAgentOwner).not.toHaveBeenCalled();
		expect(agentOwnerName(history[0])).toBe("History Owner");
	});
});

// History is delivered newest-first, which is the order the telemetry cap
// consumes. A run id can recur because several field rows share one turn.
function chatGroup(runId) {
	return { source: "ai_chat", agent_run_id: runId };
}

describe("history telemetry lazy-load scope", () => {
	it("keeps only distinct ai-chat runs, newest first", () => {
		const ids = historyTelemetryRunIDs([
			chatGroup(101),
			chatGroup(101),
			{ source: "standard_agent", agent_run_id: 200 },
			{ source: "ai_chat", agent_run_id: null },
			{ source: "", agent_run_id: 300 },
			chatGroup(102),
		]);

		expect(ids).toEqual([101, 102]);
	});

	it("caps the fetchable runs so a long history cannot fan out per row", () => {
		const groups = Array.from({ length: 75 }, (_, index) => chatGroup(1000 + index));

		const ids = historyTelemetryRunIDs(groups);

		expect(MAX_HISTORY_TELEMETRY_RUNS).toBe(20);
		expect(ids).toHaveLength(20);
		expect(ids).toEqual(Array.from({ length: 20 }, (_, index) => 1000 + index));
	});

	it("tolerates an empty or missing group list", () => {
		expect(historyTelemetryRunIDs([])).toEqual([]);
		expect(historyTelemetryRunIDs(undefined)).toEqual([]);
	});

	it("matches only the ai_chat source", () => {
		expect(isAIChatAttributed({ source: "ai_chat" })).toBe(true);
		expect(isAIChatAttributed({ source: "standard_agent" })).toBe(false);
		expect(isAIChatAttributed({})).toBe(false);
		expect(isAIChatAttributed(null)).toBe(false);
	});
});
