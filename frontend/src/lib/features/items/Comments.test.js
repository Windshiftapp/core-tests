import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { tick } from "svelte";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
	getComments: vi.fn(),
	getCannedResponses: vi.fn(),
}));

vi.mock('../../api/misc.js', () => ({
  getComments: mocks.getComments,
}))

vi.mock('../../api/configuration.js', () => ({
  cannedResponses: {
  			getAll: mocks.getCannedResponses,
  		},
}));

vi.mock('../../stores/auth.svelte.js', () => ({
  authStore: {
  		currentUser: { id: 7 },
  	},
}));

vi.mock("../../stores/notifications.js", () => ({
	subscribeToNewNotifications: () => () => {},
}));

vi.mock("../../editors/LazyMilkdownEditor.svelte", () => ({
	default: function MilkdownEditor() {},
}));

vi.mock("../../stores/i18n.svelte.js", () => ({
	i18n: { locale: "en" },
	t: (key) => key,
}));

import Comments from "./Comments.svelte";
import { getComments } from '../../api/misc.js';
import { cannedResponses } from '../../api/configuration.js';


function deferred() {
	let resolve;
	const promise = new Promise((resolvePromise) => {
		resolve = resolvePromise;
	});
	return { promise, resolve };
}

describe("Comments request ordering", () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it("does not let the initial request overwrite a newer SSE reconciliation", async () => {
		const initial = deferred();
		const comment = {
			id: 11,
			author_id: 8,
			author_name: "Editor",
			content: "Arrived while the item opened",
			created_at: "2026-08-17T08:00:00Z",
			updated_at: "2026-08-17T08:00:00Z",
			source: "user",
			is_agent: false,
			is_private: false,
		};

		mocks.getComments
			.mockImplementationOnce(() => initial.promise)
			.mockResolvedValueOnce({
				comments: [comment],
				total: 1,
				has_more: false,
			});

		render(Comments, { props: { itemId: 42 } });
		await waitFor(() => expect(mocks.getComments).toHaveBeenCalledTimes(1));

		window.dispatchEvent(
			new CustomEvent("item-comments-changed", { detail: { itemId: 42 } }),
		);

		expect(await screen.findByTestId("comment-item")).toHaveTextContent(
			"Editor",
		);
		initial.resolve({ comments: [], total: 0, has_more: false });
		await initial.promise;
		await tick();

		expect(screen.getByTestId("comment-item")).toHaveAttribute(
			"data-comment-id",
			"11",
		);
	});
});

describe("Comments render window (WI-1451)", () => {
	function makeComment(id, minutesOffset) {
		return {
			id,
			author_id: 8,
			author_name: "Author",
			content: `Comment body ${id}`,
			created_at: new Date(Date.UTC(2026, 7, 17, 8, minutesOffset)).toISOString(),
			updated_at: new Date(Date.UTC(2026, 7, 17, 8, minutesOffset)).toISOString(),
			source: "user",
			is_agent: false,
			is_private: false,
		};
	}

	beforeEach(() => {
		vi.clearAllMocks();
	});

	it("mounts a bounded window and reveals fetched comments via load more", async () => {
		const loaded = Array.from({ length: 250 }, (_, i) => makeComment(i + 1, i));
		mocks.getComments.mockResolvedValue({
			comments: loaded,
			total: 250,
			has_more: false,
		});

		render(Comments, { props: { itemId: 42 } });
		await waitFor(() => expect(screen.getAllByTestId("comment-item").length).toBe(100));

		// Only the first window of fetched comments is mounted.
		expect(screen.getAllByTestId("comment-item").length).toBe(100);
		expect(
			document.querySelector('[data-comment-id="100"]'),
		).not.toBeNull();
		expect(
			document.querySelector('[data-comment-id="101"]'),
		).toBeNull();

		// Load more reveals from memory without another request.
		await fireEvent.click(screen.getByTestId("comments-load-more"));
		await tick();

		expect(screen.getAllByTestId("comment-item").length).toBe(200);
		expect(mocks.getComments).toHaveBeenCalledTimes(1);

		// Revealing the rest exhausts the window; the button disappears when
		// nothing is hidden and no server page remains.
		await fireEvent.click(screen.getByTestId("comments-load-more"));
		await tick();

		expect(screen.getAllByTestId("comment-item").length).toBe(250);
		expect(screen.queryByTestId("comments-load-more")).toBeNull();
	});
});

describe("Comments canned response visibility", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		mocks.getComments.mockResolvedValue({ comments: [], total: 0, has_more: false });
		mocks.getCannedResponses.mockResolvedValue([
			{ id: 1, name: "greeting", body: "Hello there", is_private: false },
		]);
	});

	it("shows the canned response picker for external requests", async () => {
		render(Comments, {
			props: { itemId: 42, workspaceId: 5, isExternalRequest: true },
		});

		expect(await screen.findByTestId("canned-response-picker")).toBeTruthy();
	});

	it("hides the canned response picker on internal items", async () => {
		render(Comments, { props: { itemId: 42, workspaceId: 5 } });

		await waitFor(() => expect(mocks.getComments).toHaveBeenCalled());
		expect(mocks.getCannedResponses).not.toHaveBeenCalled();
		expect(screen.queryByTestId("canned-response-picker")).toBeNull();
	});
});

describe("Comments timestamp tooltip", () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it("exposes the absolute timestamp in the comment time tooltip", async () => {
		mocks.getComments.mockResolvedValue({
			comments: [
				{
					id: 5,
					author_id: 8,
					author_name: "Author",
					content: "Hello",
					created_at: "2026-08-17T08:00:00Z",
					updated_at: "2026-08-17T08:00:00Z",
					source: "user",
					is_agent: false,
					is_private: false,
				},
			],
			total: 1,
			has_more: false,
		});

		render(Comments, { props: { itemId: 42 } });

		const time = await screen.findByTestId("comment-time");
		await fireEvent.focus(time.parentElement);

		expect(await screen.findByTestId("tooltip")).toHaveTextContent(
			"Aug 17, 2026 at 8:00 AM UTC",
		);
	});
});

describe("Comments internal note visibility", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		mocks.getComments.mockResolvedValue({ comments: [], total: 0, has_more: false });
	});

	it("shows the internal note toggle for external requests", async () => {
		render(Comments, { props: { itemId: 42, isExternalRequest: true } });

		expect(await screen.findByTestId("comment-internal-note")).toBeTruthy();
	});

	it("hides the internal note toggle for internal items", async () => {
		render(Comments, { props: { itemId: 42 } });

		await waitFor(() => expect(mocks.getComments).toHaveBeenCalled());
		expect(screen.queryByTestId("comment-internal-note")).toBeNull();
	});
});
