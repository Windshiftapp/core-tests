import { describe, expect, it, vi } from "vitest";
import { createAsyncLoader } from "./createAsyncLoader.svelte.js";

describe("createAsyncLoader", () => {
	it("silently discards an aborted load during navigation", async () => {
		const consoleError = vi
			.spyOn(console, "error")
			.mockImplementation(() => {});
		const loader = createAsyncLoader(() =>
			Promise.reject(
				new DOMException("The document was unloaded", "AbortError"),
			),
		);

		await loader.load();

		expect(loader.loading).toBe(false);
		expect(loader.error).toBeNull();
		expect(loader.data).toEqual([]);
		expect(consoleError).not.toHaveBeenCalled();
		consoleError.mockRestore();
	});

	it("discards a failed load after its owner is disposed", async () => {
		const consoleError = vi
			.spyOn(console, "error")
			.mockImplementation(() => {});
		let rejectLoad;
		const loader = createAsyncLoader(
			() =>
				new Promise((_, reject) => {
					rejectLoad = reject;
				}),
		);

		const load = loader.load();
		loader.dispose();
		rejectLoad(new TypeError("Failed to fetch"));
		await load;

		expect(loader.loading).toBe(false);
		expect(loader.error).toBeNull();
		expect(loader.data).toEqual([]);
		expect(consoleError).not.toHaveBeenCalled();
		consoleError.mockRestore();
	});

	it("supersedes an in-flight load when reloaded", async () => {
		let resolveFirst;
		let resolveSecond;
		let call = 0;
		const loader = createAsyncLoader(() => {
			call += 1;
			if (call === 1) {
				return new Promise((resolve) => {
					resolveFirst = resolve;
				});
			}
			return new Promise((resolve) => {
				resolveSecond = resolve;
			});
		});

		const first = loader.load();
		const second = loader.reload();

		// The superseded result must never reach the loader.
		resolveFirst([{ id: 1 }]);
		await first;
		expect(loader.data).toEqual([]);

		resolveSecond([{ id: 2 }]);
		await second;
		expect(loader.data).toEqual([{ id: 2 }]);
		expect(loader.loading).toBe(false);
	});

	it("keeps loading true until the superseding reload settles", async () => {
		let resolveSecond;
		let call = 0;
		const loader = createAsyncLoader(() => {
			call += 1;
			if (call === 1) return Promise.resolve([{ id: 1 }]);
			return new Promise((resolve) => {
				resolveSecond = resolve;
			});
		});

		const first = loader.load();
		const second = loader.reload();
		await first;

		expect(loader.loading).toBe(true);
		resolveSecond([{ id: 2 }]);
		await second;
		expect(loader.loading).toBe(false);
		expect(loader.data).toEqual([{ id: 2 }]);
	});
});
