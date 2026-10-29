import { afterEach, describe, expect, it, vi } from "vitest";

import { API_SPEC_VERSIONS, filterGroups, groupOperationsByTag, loadSpec, operationRequiredScopes, resolveOperationParameters } from "./openapi-store.svelte.js";

afterEach(() => {
	vi.restoreAllMocks();
});

describe("API documentation versions", () => {
	it("defaults browser documentation to the session-mounted v2 contract", async () => {
		const json = vi.fn().mockResolvedValue({ openapi: "3.0.3" });
		vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, json }));

		await expect(loadSpec()).resolves.toEqual({ openapi: "3.0.3" });
		expect(fetch).toHaveBeenCalledWith("/api/v2/openapi.json", {
			headers: { Accept: "application/json" },
		});
		expect(API_SPEC_VERSIONS.map(({ value }) => value)).toEqual(["v2", "v1"]);
	});

	it("loads the explicitly selected compatibility document", async () => {
		vi.stubGlobal(
			"fetch",
			vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) }),
		);

		await loadSpec("/rest/api/v1/openapi.json");

		expect(fetch).toHaveBeenCalledWith("/rest/api/v1/openapi.json", {
			headers: { Accept: "application/json" },
		});
	});

	it("finds operations by tag, method, path, ID, or summary", () => {
		const groups = groupOperationsByTag({
			paths: {
				"/items/{item_id}": {
					patch: { tags: ["Work items"], summary: "Update a work item" },
				},
			},
		});

		for (const query of [
			"work items",
			"patch",
			"/items/",
			"op-patch-items-item_id",
			"update",
		]) {
			expect(filterGroups(groups, query)[0]?.operations).toHaveLength(1);
		}
		expect(filterGroups(groups, "users")).toEqual([]);
	});

	it("resolves inherited path parameters and operation overrides", () => {
		const spec = {
			components: {
				parameters: {
					ItemID: {
						name: "item_id",
						in: "path",
						required: true,
						description: "The item identifier.",
						schema: { type: "integer" },
					},
				},
			},
			paths: {
				"/items/{item_id}": {
					parameters: [{ $ref: "#/components/parameters/ItemID" }],
					get: {
						tags: ["Work items"],
						summary: "Get item",
						parameters: [{ name: "item_id", in: "path", description: "Operation override" }],
					},
				},
			},
		};
		const entry = groupOperationsByTag(spec)[0].operations[0];

		expect(resolveOperationParameters(spec, entry)).toEqual([
			{ name: "item_id", in: "path", description: "Operation override" },
		]);
	});

	it("reads v2 required scopes and preserves the v1 fallback", () => {
		expect(operationRequiredScopes({
			security: [{ BearerAuth: [] }],
			"x-required-scopes": ["tests:read"],
		})).toEqual(["tests:read"]);
		expect(operationRequiredScopes({ security: [{ BearerAuth: ["items:read"] }] }))
			.toEqual(["items:read"]);
	});

	it("surfaces alternative scopes accepted by a route", () => {
		expect(operationRequiredScopes({
			security: [{ BearerAuth: [] }],
			"x-required-any-scopes": ["items:read", "pages:read"],
		})).toEqual(["items:read", "pages:read"]);
	});
});
