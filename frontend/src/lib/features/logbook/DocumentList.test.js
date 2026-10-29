import { fireEvent, render, screen } from "@testing-library/svelte";
import { tick } from "svelte";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { logbook } from '../../api/logbook.js';


vi.mock("../../router.js", () => ({ navigate: vi.fn() }));
vi.mock("../../stores/i18n.svelte.js", () => ({ t: (key) => key }));
vi.mock("../../stores/toasts.svelte.js", () => ({
	successToast: vi.fn(),
	errorToast: vi.fn(),
}));
vi.mock("../../composables/useConfirm.js", () => ({ confirm: vi.fn() }));
vi.mock('../../api/logbook.js', () => ({
  logbook: {
  			keywordSearch: vi.fn(),
  			getDocumentThumbnailUrl: (id) => `/thumb/${id}`,
  			getDocumentFileUrl: (id) => `/file/${id}`,
  			createNote: vi.fn(),
  			archiveDocument: vi.fn(),
  		},
}));
vi.mock("../../stores/logbook.svelte.js", () => ({
	logbookStore: {
		documents: [],
		totalDocuments: 2,
		documentsLoading: false,
		buckets: [],
		loadDocuments: vi.fn(),
		loadAllDocuments: vi.fn(),
	},
}));


const { logbookStore } = await import("../../stores/logbook.svelte.js");
const { default: DocumentList } = await import("./DocumentList.svelte");

function deferred() {
	let resolve;
	let reject;
	const promise = new Promise((res, rej) => {
		resolve = res;
		reject = rej;
	});
	return { promise, resolve, reject };
}

function twoReadyDocuments() {
	return [
		{ id: 1, title: "Alpha", status: "ready", source_type: "upload" },
		{ id: 2, title: "Beta", status: "ready", source_type: "note" },
	];
}

async function typeSearch(value) {
	await fireEvent.input(screen.getByTestId("logbook-search"), {
		target: { value },
	});
}

describe("DocumentList search ownership (WI-1616)", () => {
	beforeEach(() => {
		vi.useFakeTimers();
		vi.clearAllMocks();
		logbookStore.documents = twoReadyDocuments();
	});

	afterEach(() => {
		vi.useRealTimers();
	});

	it("keeps the newest query results when an older search resolves last", async () => {
		const slow = deferred();
		const fast = deferred();
		logbook.keywordSearch
			.mockImplementationOnce(() => slow.promise)
			.mockImplementationOnce(() => fast.promise);

		render(DocumentList, { props: { activeBucketId: null } });

		await typeSearch("alpha");
		await vi.advanceTimersByTimeAsync(300);
		await typeSearch("beta");
		await vi.advanceTimersByTimeAsync(300);

		fast.resolve({ data: [{ document_id: 2 }] });
		await vi.advanceTimersByTimeAsync(0);
		await tick();
		expect(screen.getByTestId("logbook-document-2")).toBeInTheDocument();
		expect(screen.queryByTestId("logbook-document-1")).not.toBeInTheDocument();

		// The obsolete first search resolves last and must be discarded.
		slow.resolve({ data: [{ document_id: 1 }] });
		await vi.advanceTimersByTimeAsync(0);
		await tick();
		expect(screen.queryByTestId("logbook-document-1")).not.toBeInTheDocument();
		expect(screen.getByTestId("logbook-document-2")).toBeInTheDocument();
	});

	it("clears the filter immediately when the query is emptied", async () => {
		logbook.keywordSearch.mockResolvedValueOnce({ data: [{ document_id: 2 }] });
		render(DocumentList, { props: { activeBucketId: null } });

		await typeSearch("beta");
		await vi.advanceTimersByTimeAsync(300);
		await tick();
		expect(screen.getByTestId("logbook-document-2")).toBeInTheDocument();
		expect(screen.queryByTestId("logbook-document-1")).not.toBeInTheDocument();

		await typeSearch("");
		await tick();
		expect(screen.getByTestId("logbook-document-1")).toBeInTheDocument();
		expect(screen.getByTestId("logbook-document-2")).toBeInTheDocument();
	});

	it("drops an in-flight search when the bucket changes", async () => {
		const slow = deferred();
		logbook.keywordSearch.mockImplementationOnce(() => slow.promise);

		const { rerender } = render(DocumentList, { props: { activeBucketId: 1 } });
		await typeSearch("beta");
		await vi.advanceTimersByTimeAsync(300);

		await rerender({ activeBucketId: 2 });
		await tick();

		slow.resolve({ data: [{ document_id: 2 }] });
		await tick();

		expect(screen.getByTestId("logbook-document-1")).toBeInTheDocument();
		expect(screen.getByTestId("logbook-document-2")).toBeInTheDocument();
	});

	it("does not start a search when unmounted before the debounce fires", async () => {
		const { unmount } = render(DocumentList, { props: { activeBucketId: null } });
		await typeSearch("beta");
		unmount();
		await vi.advanceTimersByTimeAsync(300);

		expect(logbook.keywordSearch).not.toHaveBeenCalled();
	});
});

describe("DocumentList processing refresh scheduling (WI-1617)", () => {
	beforeEach(() => {
		vi.useFakeTimers();
		vi.clearAllMocks();
		logbookStore.documents = [
			{ id: 1, title: "Alpha", status: "processing", source_type: "upload" },
		];
	});

	afterEach(() => {
		vi.useRealTimers();
	});

	it("never overlaps refreshes and stops polling on unmount", async () => {
		const first = deferred();
		logbookStore.loadAllDocuments.mockReturnValueOnce(first.promise);

		const { unmount } = render(DocumentList, { props: { activeBucketId: null } });

		await vi.advanceTimersByTimeAsync(3000);
		expect(logbookStore.loadAllDocuments).toHaveBeenCalledTimes(1);

		// The first refresh is still running, so the next tick must not overlap.
		await vi.advanceTimersByTimeAsync(3000);
		expect(logbookStore.loadAllDocuments).toHaveBeenCalledTimes(1);

		first.resolve({ data: [], pagination: { total: 0 } });
		await tick();
		await vi.advanceTimersByTimeAsync(3000);
		expect(logbookStore.loadAllDocuments).toHaveBeenCalledTimes(2);

		unmount();
		await vi.advanceTimersByTimeAsync(9000);
		expect(logbookStore.loadAllDocuments).toHaveBeenCalledTimes(2);
	});

	it("skips a scheduled refresh while the page is hidden", async () => {
		const hiddenDescriptor = Object.getOwnPropertyDescriptor(Document.prototype, "hidden");
		Object.defineProperty(document, "hidden", { configurable: true, value: true });

		try {
			render(DocumentList, { props: { activeBucketId: null } });
			await vi.advanceTimersByTimeAsync(3000);
			expect(logbookStore.loadAllDocuments).not.toHaveBeenCalled();
		} finally {
			if (hiddenDescriptor) {
				Object.defineProperty(Document.prototype, "hidden", hiddenDescriptor);
			} else {
				delete document.hidden;
			}
		}
	});
});
