import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
	getAll: vi.fn(),
	get: vi.fn(),
	create: vi.fn(),
	update: vi.fn(),
	delete: vi.fn(),
	preview: vi.fn(),
}));

vi.mock('../api/configuration.js', () => ({
  cannedResponses: mocks,
}));

vi.mock("../stores/i18n.svelte.js", () => ({
	t: (key, params) => (params && params.name ? `${key}:${params.name}` : key),
}));

vi.mock("../stores/toasts.svelte.js", () => ({
	errorToast: vi.fn(),
	successToast: vi.fn(),
}));

import WorkspaceCannedResponses from "./WorkspaceCannedResponses.svelte";
import { cannedResponses } from '../api/configuration.js';

const existing = {
	id: 11,
	workspace_id: 1,
	name: "greeting",
	body: "Hello {{requester.name}}",
	is_private: false,
	is_active: true,
	used_count: 3,
};

describe("WorkspaceCannedResponses", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		mocks.getAll.mockResolvedValue([existing]);
	});

	it("lists responses with visibility and usage", async () => {
		render(WorkspaceCannedResponses, { props: { workspaceId: 1 } });

		expect(await screen.findByTestId("canned-response-list")).toBeInTheDocument();
		expect(mocks.getAll).toHaveBeenCalledWith(1, true);
		const row = screen.getByTestId("canned-response-row");
		expect(row).toHaveTextContent("greeting");
		expect(row).toHaveTextContent("cannedResponses.public");
		expect(row).toHaveTextContent("3");
	});

	it("creates a canned response through the editor modal", async () => {
		mocks.create.mockResolvedValue({ ...existing, id: 12, name: "closing" });

		render(WorkspaceCannedResponses, { props: { workspaceId: 1 } });
		await fireEvent.click(await screen.findByTestId("canned-response-add"));
		await screen.findByTestId("canned-response-editor");

		await fireEvent.input(document.querySelector("#canned-response-name"), {
			target: { value: "closing" },
		});
		await fireEvent.input(document.querySelector("#canned-response-body"), {
			target: { value: "Kind regards" },
		});
		await fireEvent.click(screen.getByTestId("canned-response-save"));

		await waitFor(() => {
			expect(mocks.create).toHaveBeenCalledWith(1, { name: "closing", body: "Kind regards", is_private: false });
		});
		await waitFor(() => {
			expect(mocks.getAll).toHaveBeenCalledTimes(2);
		});
	});

	it("archives through the editor instead of deleting", async () => {
		mocks.update.mockResolvedValue({ ...existing, is_active: false });

		render(WorkspaceCannedResponses, { props: { workspaceId: 1 } });
		await fireEvent.click(await screen.findByTestId("canned-response-edit"));
		await screen.findByTestId("canned-response-editor");

		await fireEvent.click(
			screen.getByTestId("canned-response-active").querySelector('input[type="checkbox"]'),
		);
		await fireEvent.click(screen.getByTestId("canned-response-save"));

		await waitFor(() => {
			expect(mocks.update).toHaveBeenCalledWith(1, 11, {
				name: "greeting",
				body: "Hello {{requester.name}}",
				is_private: false,
				is_active: false,
			});
		});
		expect(mocks.delete).not.toHaveBeenCalled();
	});

	it("deletes after confirmation", async () => {
		mocks.delete.mockResolvedValue(existing);

		render(WorkspaceCannedResponses, { props: { workspaceId: 1 } });
		await fireEvent.click(await screen.findByTestId("canned-response-delete"));

		// The confirm dialog asks with the response's name.
		expect(
			await screen.findByText("cannedResponses.deleteMessage:greeting"),
		).toBeInTheDocument();

		await fireEvent.click(screen.getByText("cannedResponses.delete"));
		await waitFor(() => {
			expect(mocks.delete).toHaveBeenCalledWith(1, 11);
		});
	});

	it("rejects saving without a body", async () => {
		render(WorkspaceCannedResponses, { props: { workspaceId: 1 } });
		await fireEvent.click(await screen.findByTestId("canned-response-add"));
		await screen.findByTestId("canned-response-editor");

		await fireEvent.input(document.querySelector("#canned-response-name"), {
			target: { value: "only-a-name" },
		});

		expect(screen.getByTestId("canned-response-save")).toBeDisabled();
		expect(mocks.create).not.toHaveBeenCalled();
	});
});
