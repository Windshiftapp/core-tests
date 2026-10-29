import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { writable } from "svelte/store";

vi.mock('../api/configuration.js', () => ({
  itemTypes: {
  			getAll: vi.fn(),
  			create: vi.fn(),
  			update: vi.fn(),
  			delete: vi.fn(),
  		},
  hierarchyLevels: {
  			getAll: vi.fn(),
  		},
}))

vi.mock('../api/objectTranslations.js', () => ({
  objectTranslations: {
  			list: vi.fn(),
  			upsert: vi.fn(),
  			delete: vi.fn(),
  		},
}));

vi.mock("../stores/i18n.svelte.js", () => ({
	i18n: {
		locale: "de",
		supportedLocales: [
			{ code: "en", name: "English" },
			{ code: "de", name: "Deutsch" },
		],
	},
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

vi.mock("../stores/permissions.svelte.js", () => ({
	isSystemAdmin: writable(true),
}));

vi.mock("../composables/useConfirm.js", () => ({
	confirm: vi.fn(),
}));

import { hierarchyLevels, itemTypes } from '../api/configuration.js';
import { objectTranslations } from '../api/objectTranslations.js';
import { errorToast } from "../stores/toasts.svelte.js";
import ItemTypeManager from "./ItemTypeManager.svelte";

describe("ItemTypeManager save errors", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		itemTypes.getAll.mockResolvedValue([]);
		hierarchyLevels.getAll.mockResolvedValue([{ level: 3, name: "Task" }]);
		objectTranslations.list.mockResolvedValue([]);
		objectTranslations.upsert.mockResolvedValue({
			object_type: "item_type",
			object_id: 42,
			field: "name",
			locale: "de",
			source: "instance",
			value: "Benutzerdefinierter Vorgangstyp",
		});
	});

	it("shows a toast above the open create modal when saving fails", async () => {
		itemTypes.create.mockRejectedValue(
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
				"Failed to save item type: Item type with this name already exists",
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
		expect(itemTypes.create).not.toHaveBeenCalled();
		expect(screen.getByRole("dialog")).toBeInTheDocument();
	});

	it("renames a custom item type through the active-locale override", async () => {
		itemTypes.getAll.mockResolvedValue([
			{
				id: 42,
				name: "Custom item type",
				display_name: "Benutzerdefinierter Typ",
				description: "Canonical description",
				display_description: "Deutsche Beschreibung",
				icon: "FileText",
				color: "#3b82f6",
				hierarchy_level: 3,
				sort_order: 1,
				is_default: false,
			},
		]);
		itemTypes.update.mockResolvedValue({});

		render(ItemTypeManager);

		const row = await screen.findByTestId("item-type-row-42");
		await fireEvent.click(row.querySelector("button"));
		await fireEvent.click(await screen.findByText("common.edit"));

		const localizedName = await screen.findByTestId("localized-object-name-de");
		expect(localizedName).toHaveValue("Benutzerdefinierter Typ");
		await fireEvent.input(localizedName, {
			target: { value: "Benutzerdefinierter Vorgangstyp" },
		});
		await fireEvent.click(screen.getByTestId("dialog-confirm"));

		await waitFor(() => {
			expect(objectTranslations.upsert).toHaveBeenCalledWith(
				"item_type",
				42,
				"name",
				"de",
				"Benutzerdefinierter Vorgangstyp",
			);
		});
		expect(itemTypes.update).toHaveBeenCalledWith(
			42,
			expect.objectContaining({ name: "Custom item type" }),
		);
	});
});
