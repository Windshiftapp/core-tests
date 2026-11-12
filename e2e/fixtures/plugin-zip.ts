import * as fs from "node:fs";
import * as path from "node:path";
import { type APIRequestContext, expect } from "./context-path";

const BASE_URL = process.env.BASE_URL || "http://localhost:8080";

// CRC-32 (IEEE) — required even for STORED zip entries.
const CRC_TABLE = (() => {
	const table = new Uint32Array(256);
	for (let i = 0; i < 256; i++) {
		let c = i;
		for (let bit = 0; bit < 8; bit++) {
			c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
		}
		table[i] = c >>> 0;
	}
	return table;
})();

function crc32(data: Uint8Array): number {
	let crc = 0xffffffff;
	for (const byte of data) {
		crc = CRC_TABLE[(crc ^ byte) & 0xff] ^ (crc >>> 8);
	}
	return (crc ^ 0xffffffff) >>> 0;
}

interface ZipEntry {
	name: string;
	data: Uint8Array;
}

// Minimal STORED (uncompressed) zip writer — enough to hand the plugin
// uploader a well-formed archive without a zip dependency.
function buildZip(entries: ZipEntry[]): Uint8Array {
	const encoder = new TextEncoder();
	const chunks: Uint8Array[] = [];
	const central: Uint8Array[] = [];
	let offset = 0;

	const u16 = (value: number) => new Uint8Array([value & 0xff, (value >>> 8) & 0xff]);
	const u32 = (value: number) =>
		new Uint8Array([value & 0xff, (value >>> 8) & 0xff, (value >>> 16) & 0xff, (value >>> 24) & 0xff]);
	const concat = (...parts: Uint8Array[]) => {
		const total = parts.reduce((sum, part) => sum + part.length, 0);
		const out = new Uint8Array(total);
		let position = 0;
		for (const part of parts) {
			out.set(part, position);
			position += part.length;
		}
		return out;
	};

	for (const entry of entries) {
		const name = encoder.encode(entry.name);
		const checksum = crc32(entry.data);
		const localStart = offset;

		chunks.push(
			concat(
				u32(0x04034b50),
				u16(20),
				u16(0),
				u16(0),
				u16(0),
				u16(0),
				u32(checksum),
				u32(entry.data.length),
				u32(entry.data.length),
				u16(name.length),
				u16(0),
				name,
				entry.data,
			),
		);
		offset += 30 + name.length + entry.data.length;

		central.push(
			concat(
				u32(0x02014b50),
				u16(20),
				u16(20),
				u16(0),
				u16(0),
				u16(0),
				u16(0),
				u32(checksum),
				u32(entry.data.length),
				u32(entry.data.length),
				u16(name.length),
				u16(0),
				u16(0),
				u16(0),
				u16(0),
				u32(0),
				u32(localStart),
				name,
			),
		);
	}

	const centralSize = central.reduce((sum, part) => sum + part.length, 0);
	const eocd = concat(
		u32(0x06054b50),
		u16(0),
		u16(0),
		u16(entries.length),
		u16(entries.length),
		u32(centralSize),
		u32(offset),
		u16(0),
	);
	return concat(...chunks, ...central, eocd);
}

// Smallest WebAssembly module that the plugin host compiles: one exported
// `init` function with an empty body. Pro capability plugins carry no logic —
// the manifest is the payload.
const MINIMAL_WASM_BASE64 = "AGFzbQEAAAABBAFgAAADAgEABwgBBGluaXQAAAoEAQIACw==";

/**
 * Upload a capability-only plugin (stub wasm + manifest) through the admin
 * plugin endpoint so core exposes the capability via /api/features.
 */
export async function installCapabilityPlugin(
	request: APIRequestContext,
	name: string,
	capabilities: string[],
): Promise<void> {
	await uploadStubPlugin(request, {
		name,
		version: "1.0.0",
		entryPoint: "plugin.wasm",
		capabilities,
	});
}

/**
 * Remove every capability plugin from a previous run. Uploaded plugins persist
 * on the server's plugin directory across restarts, so specs that assert the
 * absence of a capability must clear leftovers first.
 */
export async function deleteCapabilityPlugins(
	request: APIRequestContext,
	prefix: string,
): Promise<void> {
	const response = await request.get(`${BASE_URL}/api/plugins`, {
		headers: { "Sec-Fetch-Site": "same-origin" },
	});
	if (!response.ok()) return;
	const body = (await response.json()) as
		| Array<{ name?: string; manifest?: { name?: string } }>
		| { data?: Array<{ name?: string; manifest?: { name?: string } }> };
	const plugins = Array.isArray(body) ? body : (body.data ?? []);
	for (const plugin of plugins) {
		const name = plugin.manifest?.name ?? plugin.name;
		if (name && name.startsWith(prefix)) {
			await request.delete(`${BASE_URL}/api/plugins/${name}`, {
				headers: { "Sec-Fetch-Site": "same-origin" },
			});
		}
	}
}

function stubWasm(): Uint8Array {
	return Uint8Array.from(atob(MINIMAL_WASM_BASE64), (c) => c.charCodeAt(0));
}

/**
 * Upload a plugin whose manifest declares routes with explicit auth so the
 * module settings page can render the per-route badge.
 */
export async function installRouteAuthPlugin(
	request: APIRequestContext,
	name: string,
	routes: Array<{ method?: string; path: string; auth?: string; description?: string }>,
): Promise<void> {
	await uploadStubPlugin(request, {
		name,
		version: "1.0.0",
		entryPoint: "plugin.wasm",
		routes,
	});
}

/**
 * Upload a plugin whose manifest declares host-function permissions so the
 * module settings page can render the requested/granted badge per permission.
 */
export async function installPermissionPlugin(
	request: APIRequestContext,
	name: string,
	permissions: string[],
): Promise<void> {
	await uploadStubPlugin(request, {
		name,
		version: "1.0.0",
		entryPoint: "plugin.wasm",
		permissions,
	});
}

// uploadStubPlugin packs a manifest plus the stub wasm and POSTs it to the admin
// upload endpoint, which every capability/route/permission fixture shares.
async function uploadStubPlugin(
	request: APIRequestContext,
	manifest: Record<string, unknown>,
): Promise<void> {
	const name = String(manifest.name);
	const zip = buildZip([
		{ name: "manifest.json", data: new TextEncoder().encode(JSON.stringify(manifest)) },
		{ name: "plugin.wasm", data: stubWasm() },
	]);
	await uploadZip(request, `${name}.zip`, zip);
}

/**
 * Upload the real editor.markdown plugin from the core checkout so the spec
 * exercises the shipped editor.html/editor.js, not a copy. The plugin is
 * frontend-only, so the shared stub stands in for the WASM entry point.
 */
export async function installEditorPluginFromCore(
	request: APIRequestContext,
	coreDir: string,
	pluginName: string,
): Promise<void> {
	const root = path.join(coreDir, "plugin-src", pluginName);
	const manifest = JSON.parse(
		fs.readFileSync(path.join(root, "manifest.json"), "utf8"),
	) as Record<string, unknown>;
	const encoder = new TextEncoder();
	const entries: ZipEntry[] = [
		{ name: "manifest.json", data: encoder.encode(JSON.stringify(manifest)) },
		{ name: "plugin.wasm", data: stubWasm() },
	];
	for (const asset of ["editor.html", "editor.js", "editor.css"]) {
		entries.push({
			name: `assets/${asset}`,
			data: new Uint8Array(fs.readFileSync(path.join(root, "assets", asset))),
		});
	}
	await uploadZip(request, `${pluginName}.zip`, buildZip(entries));
}

/**
 * Upload a plugin that declares per-prefix asset auth policies, plus the given
 * assets (paths relative to the plugin's assets directory). Used by the asset
 * auth browser spec.
 */
export async function installAssetPlugin(
	request: APIRequestContext,
	name: string,
	policies: Array<{ path: string; auth: string }>,
	assets: Record<string, string>,
): Promise<void> {
	const encoder = new TextEncoder();
	const entries: ZipEntry[] = [
		{
			name: "manifest.json",
			data: encoder.encode(
				JSON.stringify({ name, version: "1.0.0", entryPoint: "plugin.wasm", assets: policies }),
			),
		},
		{ name: "plugin.wasm", data: stubWasm() },
	];
	for (const [assetPath, content] of Object.entries(assets)) {
		entries.push({ name: `assets/${assetPath}`, data: encoder.encode(content) });
	}
	await uploadZip(request, `${name}.zip`, buildZip(entries));
}

async function uploadZip(
	request: APIRequestContext,
	filename: string,
	zip: Uint8Array,
): Promise<void> {
	const response = await request.post(`${BASE_URL}/api/plugins/upload`, {
		headers: { "Sec-Fetch-Site": "same-origin" },
		multipart: {
			plugin: {
				name: filename,
				mimeType: "application/zip",
				buffer: Buffer.from(zip),
			},
		},
	});
	expect(
		response.ok(),
		`plugin upload failed (${response.status()}): ${await response.text()}`,
	).toBeTruthy();
}
