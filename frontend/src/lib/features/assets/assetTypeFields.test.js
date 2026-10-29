import { describe, expect, it, vi } from "vitest";
import { loadAssetTypeFields } from "./assetTypeFields.js";

const yieldToEventLoop = () => Promise.resolve();

describe("loadAssetTypeFields (WI-1622)", () => {
	it("loads every type with bounded concurrency", async () => {
		const types = Array.from({ length: 5 }, (_, index) => ({ id: index + 1 }));
		let inFlight = 0;
		let maxInFlight = 0;

		const getFields = async (typeId) => {
			inFlight += 1;
			maxInFlight = Math.max(maxInFlight, inFlight);
			await yieldToEventLoop();
			inFlight -= 1;
			return [{ custom_field_id: typeId, name: `field-${typeId}` }];
		};

		const fields = await loadAssetTypeFields(types, getFields, { concurrency: 2 });

		expect(maxInFlight).toBe(2);
		expect(fields.map((field) => field.custom_field_id)).toEqual([1, 2, 3, 4, 5]);
	});

	it("deduplicates fields by id and preserves type order", async () => {
		const types = [{ id: 1 }, { id: 2 }, { id: 3 }];
		const byType = {
			1: [
				{ custom_field_id: 10, name: "a" },
				{ custom_field_id: 20, name: "b" },
			],
			2: [
				{ custom_field_id: 20, name: "b-duplicate" },
				{ custom_field_id: 30, name: "c" },
			],
			3: [{ custom_field_id: 10, name: "a-duplicate" }],
		};

		const fields = await loadAssetTypeFields(types, async (id) => byType[id], {
			concurrency: 3,
		});

		expect(fields.map((field) => field.custom_field_id)).toEqual([10, 20, 30]);
		expect(fields.map((field) => field.name)).toEqual(["a", "b", "c"]);
	});

	it("stops scheduling new requests once the context is stale", async () => {
		const types = Array.from({ length: 6 }, (_, index) => ({ id: index + 1 }));
		let calls = 0;
		let stale = false;

		const getFields = async (typeId) => {
			calls += 1;
			if (typeId === 1) stale = true;
			await yieldToEventLoop();
			return [];
		};

		await loadAssetTypeFields(types, getFields, { concurrency: 2, isStale: () => stale });

		// The first in-flight request marks the context stale; no later request
		// for the obsolete context may be scheduled.
		expect(calls).toBe(1);
	});

	it("propagates a field request failure", async () => {
		const types = [{ id: 1 }, { id: 2 }];
		const failure = new Error("boom");
		const getFields = vi.fn(async (id) => {
			if (id === 1) throw failure;
			return [];
		});

		await expect(loadAssetTypeFields(types, getFields, { concurrency: 1 })).rejects.toBe(failure);
	});

	it("returns no fields for an empty type list without requesting", async () => {
		const getFields = vi.fn();

		await expect(loadAssetTypeFields([], getFields)).resolves.toEqual([]);
		expect(getFields).not.toHaveBeenCalled();
	});
});
