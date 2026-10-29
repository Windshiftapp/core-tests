import { beforeEach, describe, expect, it, vi } from "vitest";
import { logbook } from '../api/logbook.js';


vi.mock('../api/logbook.js', () => ({
  logbook: {
  			listDocuments: vi.fn(),
  			listAllDocuments: vi.fn(),
  		},
}));


const { logbookStore } = await import("./logbook.svelte.js");

function deferred() {
	let resolve;
	let reject;
	const promise = new Promise((res, rej) => {
		resolve = res;
		reject = rej;
	});
	return { promise, resolve, reject };
}

describe("logbook store document ownership (WI-1615)", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		logbookStore.resetDocuments?.();
	});

	it("keeps the newest bucket documents when an older response resolves last", async () => {
		const first = deferred();
		const second = deferred();
		logbook.listDocuments
			.mockImplementationOnce(() => first.promise)
			.mockImplementationOnce(() => second.promise);

		const firstLoad = logbookStore.loadDocuments(1);
		const secondLoad = logbookStore.loadDocuments(2);

		second.resolve({ data: [{ id: 20, title: "Beta" }], pagination: { total: 1 } });
		await secondLoad;
		expect(logbookStore.documents).toEqual([{ id: 20, title: "Beta" }]);
		expect(logbookStore.totalDocuments).toBe(1);

		// The obsolete first response must not replace the current list.
		first.resolve({ data: [{ id: 10, title: "Alpha" }], pagination: { total: 1 } });
		await firstLoad;
		expect(logbookStore.documents).toEqual([{ id: 20, title: "Beta" }]);
		expect(logbookStore.totalDocuments).toBe(1);
	});

	it("ignores a superseded rejection and keeps loading state owned by the newest load", async () => {
		const first = deferred();
		const second = deferred();
		logbook.listDocuments
			.mockImplementationOnce(() => first.promise)
			.mockImplementationOnce(() => second.promise);

		const firstLoad = logbookStore.loadDocuments(1);
		const secondLoad = logbookStore.loadDocuments(2);

		second.resolve({ data: [{ id: 20 }], pagination: { total: 1 } });
		await secondLoad;
		expect(logbookStore.documentsLoading).toBe(false);

		first.reject(new Error("stale failure"));
		await firstLoad;
		expect(logbookStore.documents).toEqual([{ id: 20 }]);
		expect(logbookStore.documentsLoading).toBe(false);
	});

	it("invalidates an in-flight bucket load when switching to all documents", async () => {
		const bucketLoad = deferred();
		const allLoad = deferred();
		logbook.listDocuments.mockImplementationOnce(() => bucketLoad.promise);
		logbook.listAllDocuments.mockImplementationOnce(() => allLoad.promise);

		const firstLoad = logbookStore.loadDocuments(1);
		const secondLoad = logbookStore.loadAllDocuments();

		allLoad.resolve({ data: [{ id: 30 }], pagination: { total: 1 } });
		await secondLoad;
		bucketLoad.resolve({ data: [{ id: 10 }], pagination: { total: 1 } });
		await firstLoad;

		expect(logbookStore.documents).toEqual([{ id: 30 }]);
		expect(logbookStore.activeBucketId).toBe(null);
	});

	it("resetDocuments drops an in-flight load so it cannot commit later", async () => {
		const pending = deferred();
		logbook.listDocuments.mockImplementationOnce(() => pending.promise);

		const load = logbookStore.loadDocuments(1);
		logbookStore.resetDocuments();
		pending.resolve({ data: [{ id: 10 }], pagination: { total: 1 } });
		await load;

		expect(logbookStore.documents).toEqual([]);
		expect(logbookStore.totalDocuments).toBe(0);
	});
});
