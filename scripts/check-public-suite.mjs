#!/usr/bin/env bun

import { readdir, readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const manifest = JSON.parse(
	await readFile(path.join(root, "suite-manifest.json"), "utf8"),
);

async function walk(directory) {
	const files = [];
	for (const entry of await readdir(directory, { withFileTypes: true })) {
		const absolute = path.join(directory, entry.name);
		if (entry.isDirectory()) files.push(...(await walk(absolute)));
		if (entry.isFile())
			files.push(path.relative(root, absolute).split(path.sep).join("/"));
	}
	return files;
}

function requireEntries(name, expected, actual) {
	const actualSet = new Set(actual);
	const missing = expected.filter((file) => !actualSet.has(file));
	if (missing.length) {
		const details = missing.map((file) => `  missing: ${file}`).join("\n");
		throw new Error(`${name} manifest mismatch:\n${details}`);
	}
}

const goFiles = [
	...(await walk(path.join(root, "internal"))),
	...(await walk(path.join(root, "tests"))),
]
	.filter((file) => file.endsWith("_test.go"))
	.sort();
const frontendFiles = (await walk(path.join(root, "frontend")))
	.filter((file) => /\.(?:test|spec)\.(?:js|ts)$/.test(file))
	.sort();
const playwrightFiles = (await walk(path.join(root, "e2e", "tests")))
	.filter((file) => file.endsWith(".spec.ts"))
	.sort();

const goPackages = new Set(goFiles.map((file) => path.dirname(file)));
requireEntries("Go fast packages", [...manifest.go.fast_packages].sort(), [
	...goPackages,
]);
requireEntries(
	"frontend fast lane",
	[...manifest.frontend.fast].sort(),
	frontendFiles,
);
const fastPlaywright = [...manifest.playwright.fast].sort();
const missingFast = fastPlaywright.filter(
	(file) => !playwrightFiles.includes(file),
);
if (missingFast.length) {
	throw new Error(
		`Fast Playwright tests are missing:\n${missingFast.map((file) => `  - ${file}`).join("\n")}`,
	);
}

const failures = [];
const allTests = [...goFiles, ...frontendFiles, ...playwrightFiles];
for (const file of allTests) {
	const source = await readFile(path.join(root, file), "utf8");
	if (file.endsWith("_test.go") && /\btime\.Sleep\s*\(/.test(source)) {
		failures.push(
			`${file}: uses time.Sleep instead of explicit synchronization`,
		);
	}
	if (file.startsWith("frontend/") && /\bsetTimeout\s*\(/.test(source)) {
		failures.push(
			`${file}: uses a real timer instead of fake time or an observable event`,
		);
	}
	if (fastPlaywright.includes(file)) {
		for (const [pattern, reason] of [
			[/\bwaitForTimeout\s*\(/, "uses waitForTimeout"],
			[
				/describe\.configure\s*\(\s*\{\s*mode:\s*["']serial["']/,
				"uses serial mode",
			],
			[
				/\b(?:test|describe)\.(?:skip|fixme|only)\s*\(/,
				"contains a skip, fixme, or only",
			],
		]) {
			if (pattern.test(source)) failures.push(`${file}: ${reason}`);
		}
	}
}

if (failures.length) {
	throw new Error(
		`Suite policy failed:\n${failures.map((failure) => `  - ${failure}`).join("\n")}`,
	);
}

const goTests = await Promise.all(
	goFiles.map((file) => readFile(path.join(root, file), "utf8")),
);
const goTopLevel = goTests.reduce(
	(count, source) =>
		count + (source.match(/^func Test[A-Za-z0-9_]+\s*\(/gm)?.length ?? 0),
	0,
);
const goSubtestSites = goTests.reduce(
	(count, source) => count + (source.match(/\bt\.Run\s*\(/g)?.length ?? 0),
	0,
);
const frontendDeclarations = (
	await Promise.all(
		frontendFiles.map((file) => readFile(path.join(root, file), "utf8")),
	)
).reduce(
	(count, source) => count + (source.match(/\b(?:it|test)\s*\(/g)?.length ?? 0),
	0,
);

console.log(
	`Suite policy passed: ${goFiles.length} Go files/${goTopLevel} top-level tests/${goSubtestSites} subtest sites; ` +
		`${frontendFiles.length} frontend files/${frontendDeclarations} declarations; ` +
		`${fastPlaywright.length} fast and ${playwrightFiles.length - fastPlaywright.length} additional Playwright files`,
);
