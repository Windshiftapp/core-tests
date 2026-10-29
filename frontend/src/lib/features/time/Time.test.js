import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { beforeEach, describe, expect, it, vi } from "vitest";

// Track which tab chunks were requested. Module factories run once per test
// file, so the import-count assertions are written to be order-safe.
const h = vi.hoisted(() => ({ loaded: [], failReports: false }));

vi.mock("../../router.js", async () => {
	const { writable } = await import("svelte/store");
	return {
		currentRoute: writable({ path: "/time", params: {} }),
		navigate: vi.fn(),
	};
});

vi.mock('../../stores/permissions.svelte.js', async () => {
	const { writable } = await import("svelte/store");
	return {
    permissionStore: writable({
    			userPermissionKeys: new Set(["customers.manage", "project.manage"]),
    		}),
    isSystemAdmin: writable(false),
  };
});

vi.mock("../../stores/i18n.svelte.js", () => ({ t: (key) => key }));
vi.mock("../../layout/NavigationSidebar.svelte", async () => ({
	default: (await import("./TimeNavStub.svelte")).default,
}));

vi.mock("./TimeEntry.svelte", async () => {
	h.loaded.push("time-entry");
	return { default: (await import("./TimeEntryStub.svelte")).default };
});
vi.mock("./Timesheet.svelte", async () => {
	h.loaded.push("timesheet");
	return { default: (await import("./TimesheetStub.svelte")).default };
});
vi.mock("./TimeCustomers.svelte", async () => {
	h.loaded.push("organizations");
	return { default: (await import("./TimeCustomersStub.svelte")).default };
});
vi.mock("./TimeProjects.svelte", async () => {
	h.loaded.push("projects");
	return { default: (await import("./TimeProjectsStub.svelte")).default };
});
vi.mock("./TimeReports.svelte", async () => {
	h.loaded.push("reports");
	if (h.failReports) throw new Error("chunk failed");
	return { default: (await import("./TimeReportsStub.svelte")).default };
});

const { currentRoute } = await import("../../router.js");
const { default: Time } = await import("./Time.svelte");

beforeEach(() => {
	h.loaded.length = 0;
	h.failReports = false;
	currentRoute.set({ path: "/time", params: {} });
});

describe("Time tab lazy loading (WI-1623)", () => {
	it("loads only the active tab on initial entry", async () => {
		render(Time);

		await screen.findByTestId("time-tab-time-entry");
		expect(h.loaded).toEqual(["time-entry"]);
		expect(screen.queryByTestId("time-tab-timesheet")).toBeNull();
		expect(screen.queryByTestId("time-tab-reports")).toBeNull();
	});

	it("loads the deep-linked tab instead of the default", async () => {
		currentRoute.set({ path: "/time/timesheet", params: {} });

		render(Time);

		await screen.findByTestId("time-tab-timesheet");
		expect(h.loaded).toEqual(["timesheet"]);
		expect(screen.queryByTestId("time-tab-time-entry")).toBeNull();
	});

	it("keeps a loaded tab available when returning to it", async () => {
		render(Time);
		await screen.findByTestId("time-tab-time-entry");

		currentRoute.set({ path: "/time/timesheet", params: {} });
		await screen.findByTestId("time-tab-timesheet");

		currentRoute.set({ path: "/time", params: {} });
		await screen.findByTestId("time-tab-time-entry");
		expect(screen.queryByTestId("time-tab-loading")).toBeNull();
	});

	it("offers a retry when a tab chunk fails to load", async () => {
		h.failReports = true;
		currentRoute.set({ path: "/time/worklogs", params: {} });

		render(Time);
		await screen.findByTestId("time-tab-error");

		h.failReports = false;
		await fireEvent.click(screen.getByTestId("time-tab-retry"));

		await screen.findByTestId("time-tab-reports");
		expect(h.loaded.filter((id) => id === "reports")).toHaveLength(2);
	});
});
