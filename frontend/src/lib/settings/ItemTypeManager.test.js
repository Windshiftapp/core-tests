import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

// jsdom does not implement the Web Animations API. Svelte 5 transitions
// call element.animate when the create modal opens.
beforeAll(() => {
	if (!Element.prototype.animate) {
		Element.prototype.animate = () => ({
			finished: Promise.resolve(),
			cancel: () => {},
			addEventListener: () => {},
			removeEventListener: () => {},
			play: () => {},
			pause: () => {},
		});
	}
});

vi.mock("../api.js", () => ({
	api: {
		itemTypes: {
			getAll: vi.fn(),
			create: vi.fn(),
			update: vi.fn(),
			delete: vi.fn(),
		},
		hierarchyLevels: {
			getAll: vi.fn(),
		},
	},
}));

vi.mock("../stores/i18n.svelte.js", () => ({
	t: (key) => {
		const messages = {
			"settings.itemTypes.failedToSave": "Failed to save item type:",
		};
		return messages[key] ?? key;
	},
}));

vi.mock("../stores/toasts.svelte.js", () => ({
	errorToast: vi.fn(),
}));

vi.mock("../composables/useConfirm.js", () => ({
	confirm: vi.fn(),
}));

import { api } from "../api.js";
import { errorToast } from "../stores/toasts.svelte.js";
import ItemTypeManager from "./ItemTypeManager.svelte";

describe("ItemTypeManager save errors", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		api.itemTypes.getAll.mockResolvedValue([]);
		api.hierarchyLevels.getAll.mockResolvedValue([{ level: 3, name: "Task" }]);
	});

	it("shows a toast above the open create modal when saving fails", async () => {
		api.itemTypes.create.mockRejectedValue(
			new Error("Item type with this name already exists"),
		);

		render(ItemTypeManager);

		const addButton = await screen.findByTestId("item-type-add");
		await waitFor(() => expect(addButton).not.toBeDisabled());
		await fireEvent.click(addButton);
		await fireEvent.input(document.querySelector("#name"), {
			target: { value: "Task" },
		});
		await fireEvent.click(screen.getByTestId("dialog-confirm"));

		await waitFor(() => {
			expect(errorToast).toHaveBeenCalledWith(
				"Item type with this name already exists",
			);
		});
		expect(screen.getByRole("dialog")).toBeInTheDocument();
		expect(document.querySelector(".error")).not.toBeInTheDocument();
	});

	it("shows missing-name validation in a toast without closing the modal", async () => {
		render(ItemTypeManager);

		const addButton = await screen.findByTestId("item-type-add");
		await waitFor(() => expect(addButton).not.toBeDisabled());
		await fireEvent.click(addButton);
		await fireEvent.click(screen.getByTestId("dialog-confirm"));

		expect(errorToast).toHaveBeenCalledWith("settings.itemTypes.nameRequired");
		expect(api.itemTypes.create).not.toHaveBeenCalled();
		expect(screen.getByRole("dialog")).toBeInTheDocument();
	});
});
