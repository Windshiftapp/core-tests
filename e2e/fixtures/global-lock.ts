import { mkdir, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

// Playwright's serial mode only orders tests inside one file, so specs that
// mutate a global singleton (the active theme) can still run concurrently in
// separate workers. Coordinate them with a directory that only one worker can
// create; the holder removes it on release.
const LOCK_ROOT = join(tmpdir(), "windshift-e2e-locks");
const ACQUIRE_TIMEOUT_MS = 90_000;
const STALE_LOCK_MS = 300_000;
const RETRY_MS = 100;

/** Acquire an exclusive lock shared by every worker on this machine. */
export async function acquireGlobalLock(name: string): Promise<() => Promise<void>> {
	const path = join(LOCK_ROOT, name);
	await mkdir(LOCK_ROOT, { recursive: true });
	const deadline = Date.now() + ACQUIRE_TIMEOUT_MS;

	for (;;) {
		try {
			await mkdir(path);
			return async () => {
				await rm(path, { recursive: true, force: true });
			};
		} catch (error) {
			if ((error as { code?: string }).code !== "EEXIST") throw error;
		}

		// A worker that crashed mid-test leaves the directory behind; only break
		// it once it is older than any single test could reasonably hold it.
		const info = await stat(path).catch(() => null);
		if (info && Date.now() - info.mtimeMs > STALE_LOCK_MS) {
			await rm(path, { recursive: true, force: true });
			continue;
		}
		if (Date.now() > deadline) {
			throw new Error(`Timed out waiting for the "${name}" global test lock`);
		}
		await new Promise((resolve) => setTimeout(resolve, RETRY_MS));
	}
}
