import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { writable } from "svelte/store";

vi.mock('../api/objectTranslations.js', () => ({
  objectTranslations: {
  			list: vi.fn(),
  			resolve: vi.fn(),
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
	t: (key) => key,
}));

vi.mock("../stores/permissions.svelte.js", () => ({
	isSystemAdmin: writable(true),
}));

import { objectTranslations } from '../api/objectTranslations.js';
import LocalizedObjectFields from "./LocalizedObjectFields.svelte";

const priorityProps = {
	objectType: "priority",
	objectId: 3,
	canonicalName: "Medium",
	canonicalDescription: "Medium priority",
	displayName: "Mittlere Priorität",
	displayDescription: "Mittlere Prioritätsstufe",
};

describe("LocalizedObjectFields", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		objectTranslations.upsert.mockResolvedValue({});
		objectTranslations.delete.mockResolvedValue(undefined);
	});

	it("removes an active-locale override and immediately reveals its resolved fallback", async () => {
		objectTranslations.list.mockResolvedValue([
			{
				object_type: "priority",
				object_id: 3,
				field: "name",
				locale: "de",
				source: "instance",
				value: "Mittlere Priorität",
			},
			{
				object_type: "priority",
				object_id: 3,
				field: "name",
				locale: "de",
				source: "system",
				value: "Mittel",
			},
		]);
		objectTranslations.resolve.mockResolvedValue([
			{ value: "Mittel", source: "system", locale: "de" },
		]);

		render(LocalizedObjectFields, { props: priorityProps });

		const primaryName = await screen.findByTestId("localized-object-name-de");
		expect(primaryName).toHaveValue("Mittlere Priorität");
		await fireEvent.click(screen.getByTestId("localized-object-overrides-toggle"));
		await fireEvent.click(screen.getByTestId("localized-object-remove-name-de"));

		await waitFor(() => expect(primaryName).toHaveValue("Mittel"));
		expect(objectTranslations.delete).toHaveBeenCalledWith(
			"priority",
			3,
			"name",
			"de",
		);
		expect(objectTranslations.resolve).toHaveBeenCalledWith("de", [
			{
				object_type: "priority",
				object_id: 3,
				field: "name",
				fallback: "Medium",
			},
		]);
	});

	it("requires an explicit override decision before changing the canonical fallback", async () => {
		objectTranslations.list.mockResolvedValue([
			{
				object_type: "priority",
				object_id: 3,
				field: "name",
				locale: "de",
				source: "instance",
				value: "Mittlere Priorität",
			},
		]);

		const { component } = render(LocalizedObjectFields, { props: priorityProps });
		await screen.findByTestId("localized-object-name-de");
		await fireEvent.click(screen.getByTestId("localized-object-canonical-toggle"));
		await fireEvent.input(screen.getByTestId("localized-object-canonical-name"), {
			target: { value: "Normal" },
		});

		expect(() => component.validate()).toThrow(
			"settings.localizedObjects.canonicalChoiceRequired",
		);

		await fireEvent.click(document.querySelector("#localized-object-canonical-decision"));
		await fireEvent.click(
			document.querySelector(
				"#localized-object-canonical-decision-option-remove-instance",
			),
		);
		expect(() => component.validate()).not.toThrow();
		await component.save();

		expect(objectTranslations.delete).toHaveBeenCalledWith(
			"priority",
			3,
			"name",
			"de",
		);
	});

	it("does not load or expose translation controls without write permission", () => {
		objectTranslations.list.mockResolvedValue([]);

		render(LocalizedObjectFields, {
			props: { ...priorityProps, canWrite: false },
		});

		expect(
			screen.getByText("settings.localizedObjects.permissionRequired"),
	).toBeInTheDocument();
		expect(screen.queryByTestId("localized-object-editor")).not.toBeInTheDocument();
		expect(objectTranslations.list).not.toHaveBeenCalled();
	});
});
