import { render, screen, waitFor } from "@testing-library/svelte";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { get } from '../api/core.js';
import { publicBoard } from '../api/publicBoard.js';


vi.mock("../api/publicBoard.js", () => ({
	publicBoard: {
		get: vi.fn(),
	},
}));

vi.mock("../stores/theme.svelte.js", () => ({
	themeStore: {
		resolvedTheme: "light",
		init: vi.fn(),
		setColorMode: vi.fn(),
	},
}));

const { publicBoard } = await import("../api/publicBoard.js");
const { default: PublicBoard } = await import("./PublicBoard.svelte");

describe("PublicBoard response contracts", () => {
	beforeEach(() => vi.clearAllMocks());

	it("renders supported status fields and warns when column counts are partial", async () => {
		publicBoard.get.mockResolvedValue({
			collection: { name: "Roadmap", description: "" },
			card_fields: [{ field_identifier: "status", field_type: "system" }],
			total_items: 501,
			loaded_items: 500,
			truncated: true,
			item_limit: 500,
			columns: [
				{
					name: "Open",
					color: "#64748b",
					items: [{ key: "ROAD-1", title: "Ship", status_name: "Open" }],
				},
			],
		});

		render(PublicBoard, { props: { slug: "roadmap" } });

		const warning = await screen.findByTestId("public-board-truncated");
		expect(warning.textContent).toContain("newest 500 of 501");
		expect(warning.textContent).toContain("Column counts are partial");
		expect(
			screen.getByTestId("public-board-card-status").textContent,
		).toContain("Open");
		expect(screen.queryByTestId("public-board-card-title")).toBeNull();
	});

	it("maps every approved card-field configuration to its public response value", async () => {
		publicBoard.get.mockResolvedValue({
			collection: { name: "Roadmap", description: "" },
			card_fields: [
				"key",
				"title",
				"status",
				"priority",
				"assignee",
				"item_type",
				"story_points",
				"due_date",
				"labels",
			].map((field_identifier, display_order) => ({
				field_identifier,
				field_type: "system",
				display_order,
			})),
			total_items: 1,
			loaded_items: 1,
			truncated: false,
			item_limit: 500,
			columns: [
				{
					name: "Open",
					color: "#64748b",
					items: [
						{
							key: "ROAD-42",
							title: "Ship the integration",
							status_name: "In progress",
							priority_name: "High",
							priority_color: "#dc2626",
							assignee_name: "Ada Lovelace",
							item_type_name: "Story",
							story_points: 8,
							due_date: "2026-08-01",
							labels: [{ name: "Release", color: "#2563eb" }],
						},
					],
				},
			],
		});

		render(PublicBoard, { props: { slug: "roadmap" } });

		const card = await screen.findByTestId("public-board-card");
		for (const expected of [
			"ROAD-42",
			"Ship the integration",
			"In progress",
			"High",
			"Story",
			"8 SP",
			"2026-08-01",
			"Release",
		]) {
			expect(card.textContent).toContain(expected);
		}
		expect(screen.getByTitle("Ada Lovelace")).toBeTruthy();
	});
});

describe("PublicBoard slug changes (WI-1612)", () => {
	beforeEach(() => vi.clearAllMocks());

	function board(name) {
		return {
			collection: { name, description: "" },
			card_fields: [],
			total_items: 0,
			loaded_items: 0,
			truncated: false,
			item_limit: 500,
			columns: [],
		};
	}

	it("reloads and replaces the board when the slug changes without remounting", async () => {
		publicBoard.get.mockImplementation((slug) => Promise.resolve(board(slug)));

		const { rerender } = render(PublicBoard, { props: { slug: "alpha" } });
		expect(await screen.findByText("alpha")).toBeInTheDocument();

		// The router reuses the component for /board/beta, so the prop changes
		// in place and the previous board must not linger.
		await rerender({ slug: "beta" });

		expect(await screen.findByText("beta")).toBeInTheDocument();
		expect(screen.queryByText("alpha")).not.toBeInTheDocument();
		expect(publicBoard.get).toHaveBeenLastCalledWith("beta", expect.anything());
	});

	it("cancels the previous slug request", async () => {
		publicBoard.get.mockImplementation((slug) => Promise.resolve(board(slug)));

		const { rerender } = render(PublicBoard, { props: { slug: "alpha" } });
		await screen.findByText("alpha");
		const alphaSignal = publicBoard.get.mock.calls[0][1].signal;

		await rerender({ slug: "beta" });
		await screen.findByText("beta");

		expect(alphaSignal.aborted).toBe(true);
	});

	it("does not let a late response from the old slug overwrite the new board", async () => {
		const pending = {};
		publicBoard.get.mockImplementation(
			(slug) => new Promise((resolve) => { pending[slug] = resolve; }),
		);

		const { rerender } = render(PublicBoard, { props: { slug: "alpha" } });
		await waitFor(() => expect(pending.alpha).toBeTypeOf("function"));

		await rerender({ slug: "beta" });
		await waitFor(() => expect(pending.beta).toBeTypeOf("function"));

		pending.beta(board("beta"));
		expect(await screen.findByText("beta")).toBeInTheDocument();

		// The obsolete alpha response resolves last and must be discarded.
		pending.alpha(board("alpha"));
		await waitFor(() => expect(publicBoard.get).toHaveBeenCalledTimes(2));
		expect(screen.queryByText("alpha")).not.toBeInTheDocument();
		expect(screen.getByText("beta")).toBeInTheDocument();
	});

	it("does not let a late failure from the old slug replace the new board", async () => {
		const pending = {};
		publicBoard.get.mockImplementation(
			(slug) => new Promise((resolve, reject) => { pending[slug] = { resolve, reject }; }),
		);

		const { rerender } = render(PublicBoard, { props: { slug: "alpha" } });
		await waitFor(() => expect(pending.alpha).toBeTypeOf("object"));

		await rerender({ slug: "beta" });
		await waitFor(() => expect(pending.beta).toBeTypeOf("object"));

		pending.beta.resolve(board("beta"));
		expect(await screen.findByText("beta")).toBeInTheDocument();

		// A stale rejection must not clear the new board or show an error page.
		pending.alpha.reject(Object.assign(new Error("gone"), { status: 404 }));
		await waitFor(() => expect(publicBoard.get).toHaveBeenCalledTimes(2));
		expect(screen.queryByTestId("public-board-not-found")).toBeNull();
		expect(screen.queryByTestId("public-board-error")).toBeNull();
		expect(screen.getByText("beta")).toBeInTheDocument();
	});
});
