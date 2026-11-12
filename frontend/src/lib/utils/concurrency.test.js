import { describe, expect, test } from "vitest";
import { runWithConcurrency, UPLOAD_CONCURRENCY } from "./concurrency.js";

describe("runWithConcurrency", () => {
	test("settles every item in input order", async () => {
		const results = await runWithConcurrency([1, 2, 3], async (n) => n * 2);

		expect(results.map((result) => result.item)).toEqual([1, 2, 3]);
		expect(results.map((result) => result.ok)).toEqual([true, true, true]);
		expect(results.map((result) => result.value)).toEqual([2, 4, 6]);
	});

	test("never exceeds the configured concurrency", async () => {
		const items = Array.from({ length: 9 }, (_, i) => i);
		let active = 0;
		let peak = 0;
		let release;
		const gate = new Promise((resolve) => {
			release = resolve;
		});

		const promise = runWithConcurrency(
			items,
			async () => {
				active += 1;
				peak = Math.max(peak, active);
				if (active >= 3) release();
				await gate;
				active -= 1;
				return true;
			},
			{ concurrency: 3 },
		);

		await promise;
		expect(peak).toBe(3);
	});

	test("defaults to the shared upload concurrency", async () => {
		const items = Array.from({ length: UPLOAD_CONCURRENCY + 4 }, (_, i) => i);
		let active = 0;
		let peak = 0;
		let release;
		const gate = new Promise((resolve) => {
			release = resolve;
		});

		await runWithConcurrency(items, async () => {
			active += 1;
			peak = Math.max(peak, active);
			if (active >= UPLOAD_CONCURRENCY) release();
			await gate;
			active -= 1;
		});

		expect(peak).toBe(UPLOAD_CONCURRENCY);
	});

	test("records a rejection without cancelling the remaining items", async () => {
		const seen = [];
		const results = await runWithConcurrency(
			[1, 2, 3, 4, 5],
			async (n) => {
				seen.push(n);
				if (n === 3) throw new Error("boom");
				return n;
			},
			{ concurrency: 2 },
		);

		expect([...seen].sort((a, b) => a - b)).toEqual([1, 2, 3, 4, 5]);
		expect(results.filter((result) => result.ok).map((result) => result.value)).toEqual([
			1, 2, 4, 5,
		]);
		const failed = results.find((result) => !result.ok);
		expect(failed.item).toBe(3);
		expect(failed.value).toBeNull();
		expect(failed.error.message).toBe("boom");
	});

	test("returns an empty array when there is nothing to run", async () => {
		await expect(runWithConcurrency([], async () => 1)).resolves.toEqual([]);
	});
});
