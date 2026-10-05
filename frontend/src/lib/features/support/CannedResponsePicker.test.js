import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
	getAll: vi.fn(),
}));

vi.mock("../../api.js", () => ({
	api: {
		cannedResponses: {
			getAll: mocks.getAll,
		},
	},
}));

vi.mock("../../stores/i18n.svelte.js", () => ({
	t: (key) => key,
}));

vi.mock("../../stores/toasts.svelte.js", () => ({
	errorToast: vi.fn(),
	successToast: vi.fn(),
}));

import CannedResponsePicker from "./CannedResponsePicker.svelte";

const responses = [
	{ id: 1, name: "greeting", body: "Hello there", is_private: false },
	{ id: 2, name: "refund-policy", body: "Our refund policy is 30 days", is_private: false },
	{ id: 3, name: "internal-escalation", body: "Escalate to on-call", is_private: true },
];

describe("CannedResponsePicker", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		mocks.getAll.mockResolvedValue(responses);
	});

	it("lists the workspace's canned responses with a search box", async () => {
		render(CannedResponsePicker, { props: { workspaceId: 5, isOpen: true, onSelect: vi.fn() } });

		expect(await screen.findByText("greeting")).toBeTruthy();
		expect(screen.getByText("refund-policy")).toBeTruthy();
		expect(screen.getByText("internal-escalation")).toBeTruthy();
		expect(screen.getByTestId("canned-response-search")).toBeTruthy();
		expect(mocks.getAll).toHaveBeenCalledWith(5);
	});

	it("filters options by name when searching", async () => {
		render(CannedResponsePicker, { props: { workspaceId: 5, isOpen: true, onSelect: vi.fn() } });
		await screen.findByText("greeting");

		await fireEvent.input(screen.getByTestId("canned-response-search"), { target: { value: "refund" } });

		expect(screen.getByText("refund-policy")).toBeTruthy();
		expect(screen.queryByText("greeting")).toBeNull();
	});

	it("shows a no-match message instead of options", async () => {
		render(CannedResponsePicker, { props: { workspaceId: 5, isOpen: true, onSelect: vi.fn() } });
		await screen.findByText("greeting");

		await fireEvent.input(screen.getByTestId("canned-response-search"), { target: { value: "zzz" } });

		expect(screen.getByText("cannedResponses.noneMatching")).toBeTruthy();
		expect(screen.queryByText("greeting")).toBeNull();
	});

	it("selecting an option hands the response to onSelect", async () => {
		const onSelect = vi.fn();
		render(CannedResponsePicker, { props: { workspaceId: 5, isOpen: true, onSelect } });
		await screen.findByText("greeting");

		await fireEvent.click(screen.getByText("refund-policy"));

		await waitFor(() => {
			expect(onSelect).toHaveBeenCalledWith(responses[1]);
		});
	});

	it("loads nothing and renders no picker without a workspace", async () => {
		render(CannedResponsePicker, { props: { workspaceId: null, isOpen: true, onSelect: vi.fn() } });
		await waitFor(() => {
			expect(mocks.getAll).not.toHaveBeenCalled();
		});
		expect(screen.queryByTestId("canned-response-picker")).toBeNull();
	});

	it("renders no picker when the workspace has no canned responses", async () => {
		mocks.getAll.mockResolvedValue([]);
		render(CannedResponsePicker, { props: { workspaceId: 5, isOpen: true, onSelect: vi.fn() } });

		await waitFor(() => {
			expect(mocks.getAll).toHaveBeenCalledWith(5);
		});
		expect(screen.queryByTestId("canned-response-picker")).toBeNull();
		expect(screen.queryByTestId("canned-response-search")).toBeNull();
	});

	it("does not render the picker until responses load", async () => {
		let resolveLoad;
		mocks.getAll.mockReturnValue(
			new Promise((resolve) => {
				resolveLoad = resolve;
			})
		);
		render(CannedResponsePicker, { props: { workspaceId: 5, isOpen: true, onSelect: vi.fn() } });

		expect(screen.queryByTestId("canned-response-picker")).toBeNull();

		resolveLoad(responses);
		expect(await screen.findByTestId("canned-response-picker")).toBeTruthy();
	});
});
