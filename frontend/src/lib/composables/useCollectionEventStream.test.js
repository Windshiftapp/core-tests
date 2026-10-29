import { render } from "@testing-library/svelte";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import Harness from "./useCollectionEventStreamHarness.svelte";

function installFakeEventSource() {
	const instances = [];
	class FakeEventSource {
		constructor(url) {
			this.url = url;
			this.listeners = new Map();
			this.closed = false;
			instances.push(this);
		}
		addEventListener(type, callback) {
			if (!this.listeners.has(type)) this.listeners.set(type, []);
			this.listeners.get(type).push(callback);
		}
		emit(type) {
			for (const callback of this.listeners.get(type) ?? []) callback();
			if (type === "error" && typeof this.onerror === "function") this.onerror();
		}
		close() {
			this.closed = true;
		}
	}
	globalThis.EventSource = FakeEventSource;
	return {
		instances,
		restore: () => {
			delete globalThis.EventSource;
		},
	};
}

describe("useCollectionEventStream (WI-1624)", () => {
	let fakeEventSource;

	beforeEach(() => {
		vi.useFakeTimers();
		fakeEventSource = installFakeEventSource();
	});

	afterEach(() => {
		vi.useRealTimers();
		fakeEventSource.restore();
	});

	it("subscribes to the collection scope and reconciles on reconnect", async () => {
		const onInvalidate = vi.fn();
		const { component } = render(Harness, {
			props: { scope: { kind: "collection", id: 5 }, onInvalidate },
		});
		expect(fakeEventSource.instances).toHaveLength(1);
		expect(fakeEventSource.instances[0].url).toContain("/api/collections/5/events");

		fakeEventSource.instances[0].emit("connected");
		expect(component.isConnected()).toBe(true);
		await vi.advanceTimersByTimeAsync(250);
		expect(onInvalidate).not.toHaveBeenCalled();

		fakeEventSource.instances[0].emit("error");
		fakeEventSource.instances[0].emit("connected");
		await vi.advanceTimersByTimeAsync(250);
		expect(onInvalidate).toHaveBeenCalledTimes(1);
	});

	it("subscribes to the workspace scope when there is no collection", async () => {
		render(Harness, { props: { scope: { kind: "workspace", id: 9 }, onInvalidate: vi.fn() } });

		expect(fakeEventSource.instances).toHaveLength(1);
		expect(fakeEventSource.instances[0].url).toContain("/api/workspaces/9/events");
	});

	it("coalesces bursts of invalidation frames into one refresh", async () => {
		const onInvalidate = vi.fn();
		render(Harness, {
			props: { scope: { kind: "collection", id: 5 }, onInvalidate },
		});

		fakeEventSource.instances[0].emit("connected");
		fakeEventSource.instances[0].emit("changed");
		fakeEventSource.instances[0].emit("changed");
		await vi.advanceTimersByTimeAsync(250);

		expect(onInvalidate).toHaveBeenCalledTimes(1);
	});

	it("marks disconnected on transport error so polling resumes", async () => {
		const { component } = render(Harness, {
			props: { scope: { kind: "collection", id: 5 }, onInvalidate: vi.fn() },
		});
		fakeEventSource.instances[0].emit("connected");
		expect(component.isConnected()).toBe(true);

		fakeEventSource.instances[0].emit("error");
		expect(component.isConnected()).toBe(false);
	});

	it("closes the stream on unmount", async () => {
		const { unmount } = render(Harness, {
			props: { scope: { kind: "collection", id: 5 }, onInvalidate: vi.fn() },
		});
		const instance = fakeEventSource.instances[0];

		unmount();

		expect(instance.closed).toBe(true);
	});

	it("does not open a stream without a scope id", async () => {
		render(Harness, { props: { scope: { kind: "collection", id: null }, onInvalidate: vi.fn() } });

		expect(fakeEventSource.instances).toHaveLength(0);
	});
});
