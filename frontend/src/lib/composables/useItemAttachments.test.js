import { render, waitFor } from "@testing-library/svelte";
import { afterEach, describe, expect, test, vi } from "vitest";

const mocks = vi.hoisted(() => ({
	getByItem: vi.fn(),
	uploadToItem: vi.fn(),
	setInternal: vi.fn(),
}));

vi.mock('../api/misc.js', () => ({
  attachments: {
  			getByItem: mocks.getByItem,
  			uploadToItem: mocks.uploadToItem,
  			setInternal: mocks.setInternal,
  		},
}));

vi.mock('../stores/attachmentStatus.svelte.js', () => ({
  attachmentStatus: {
  		enabled: true,
  		load: vi.fn(),
  	},
}));

import ItemAttachmentsHarness from "./ItemAttachmentsHarness.svelte";
import { UPLOAD_CONCURRENCY } from '../utils/concurrency.js';

afterEach(() => {
	window.dispatchEvent(new Event("pageshow"));
	mocks.getByItem.mockReset();
	mocks.uploadToItem.mockReset();
	mocks.setInternal.mockReset();
	vi.restoreAllMocks();
});

async function renderHarness(props = {}) {
	let manager;
	const view = render(ItemAttachmentsHarness, {
		props: {
			onready: (nextManager) => {
				manager = nextManager;
			},
			...props,
		},
	});
	await waitFor(() => expect(manager).toBeDefined());
	return { manager, view };
}

describe("useItemAttachments load lifecycle", () => {
	test("ignores a request failure after its component is destroyed", async () => {
		let rejectRequest;
		mocks.getByItem.mockReturnValue(
			new Promise((_, reject) => {
				rejectRequest = reject;
			}),
		);
		const errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
		const { manager, view } = await renderHarness();

		const loadPromise = manager.load();
		view.unmount();
		rejectRequest(new TypeError("Failed to fetch"));
		await loadPromise;

		expect(errorSpy).not.toHaveBeenCalled();
	});

	test("ignores a request failure while the document is unloading", async () => {
		let rejectRequest;
		mocks.getByItem.mockReturnValue(
			new Promise((_, reject) => {
				rejectRequest = reject;
			}),
		);
		const errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
		const { manager } = await renderHarness();

		const loadPromise = manager.load();
		window.dispatchEvent(new Event("pagehide"));
		rejectRequest(new TypeError("Failed to fetch"));
		await loadPromise;

		expect(errorSpy).not.toHaveBeenCalled();
	});

	test("reports a request failure while its component remains mounted", async () => {
		const requestError = new TypeError("Failed to fetch");
		mocks.getByItem.mockRejectedValue(requestError);
		const errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
		const { manager } = await renderHarness();

		await manager.load();

		expect(errorSpy).toHaveBeenCalledWith(
			"Failed to load attachments:",
			requestError,
		);
	});
});

describe("useItemAttachments visibility", () => {
	test("hides an attachment and updates the row in place", async () => {
		mocks.getByItem.mockResolvedValue({
			data: [{ id: 5, original_filename: "a.png", is_internal: false }],
			pagination: null,
		});
		mocks.setInternal.mockResolvedValue({ id: 5, original_filename: "a.png", is_internal: true });
		const { manager } = await renderHarness({ showError: vi.fn() });
		await manager.load();

		await manager.handleToggleVisibility(manager.attachments[0]);

		expect(mocks.setInternal).toHaveBeenCalledWith(5, true);
		expect(manager.attachments[0].is_internal).toBe(true);
		// The response updates the row; no list reload is needed.
		expect(mocks.getByItem).toHaveBeenCalledTimes(1);
	});

	test("reveals an internal attachment", async () => {
		mocks.getByItem.mockResolvedValue({
			data: [{ id: 6, original_filename: "b.png", is_internal: true }],
			pagination: null,
		});
		mocks.setInternal.mockResolvedValue({ id: 6, original_filename: "b.png", is_internal: false });
		const { manager } = await renderHarness({ showError: vi.fn() });
		await manager.load();

		await manager.handleToggleVisibility(manager.attachments[0]);

		expect(mocks.setInternal).toHaveBeenCalledWith(6, false);
		expect(manager.attachments[0].is_internal).toBe(false);
	});

	test("reports a failure and keeps the row unchanged", async () => {
		mocks.getByItem.mockResolvedValue({
			data: [{ id: 7, original_filename: "c.png", is_internal: false }],
			pagination: null,
		});
		mocks.setInternal.mockRejectedValue(new Error("boom"));
		const showError = vi.fn();
		vi.spyOn(console, "error").mockImplementation(() => {});
		const { manager } = await renderHarness({ showError });
		await manager.load();

		await manager.handleToggleVisibility(manager.attachments[0]);

		expect(showError).toHaveBeenCalledWith("Failed to update attachment visibility", "boom");
		expect(manager.attachments[0].is_internal).toBe(false);
	});
});

describe("useItemAttachments uploads", () => {
	test("bounds upload concurrency and keeps uploading after a failure", async () => {
		let active = 0;
		let peak = 0;
		let release;
		const gate = new Promise((resolve) => {
			release = resolve;
		});
		const uploaded = [];

		mocks.uploadToItem.mockImplementation(async (itemId, file) => {
			active += 1;
			peak = Math.max(peak, active);
			if (active >= UPLOAD_CONCURRENCY) release();
			await gate;
			active -= 1;
			if (file.name === "bad.png") throw new Error("disk full");
			uploaded.push(file.name);
			return { id: file.name };
		});
		mocks.getByItem.mockResolvedValue({ data: [], pagination: null });

		const showError = vi.fn();
		const errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
		const { manager } = await renderHarness({ showError });
		const files = Array.from({ length: 7 }, (_, i) => ({ name: `file-${i}.png` }));
		files[4] = { name: "bad.png" };

		await manager.uploadFiles({ files });

		expect(peak).toBe(UPLOAD_CONCURRENCY);
		expect(uploaded).toHaveLength(6);
		expect(uploaded).not.toContain("bad.png");
		expect(showError).toHaveBeenCalledTimes(1);
		expect(showError).toHaveBeenCalledWith("Failed to upload attachment", "disk full");
		expect(errorSpy).toHaveBeenCalledWith("Upload error:", expect.any(Error));
		expect(mocks.getByItem).toHaveBeenCalledTimes(1);
	});

	test("reloads the attachment list once for a successful batch", async () => {
		mocks.uploadToItem.mockResolvedValue({ id: 1 });
		mocks.getByItem.mockResolvedValue({ data: [], pagination: null });
		const { manager } = await renderHarness({ showError: vi.fn() });

		await manager.uploadFiles({ files: [{ name: "a.png" }, { name: "b.png" }] });

		expect(mocks.uploadToItem).toHaveBeenCalledTimes(2);
		expect(mocks.getByItem).toHaveBeenCalledTimes(1);
	});
});
