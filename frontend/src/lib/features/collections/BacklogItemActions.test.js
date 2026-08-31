import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import { afterEach, beforeAll, describe, expect, test, vi } from "vitest";

vi.mock("../../stores/i18n.svelte.js", () => ({
	t: (key, params = {}) => {
		const translations = {
			"collections.backlogItemActions": `Backlog actions for ${params.title}`,
			"collections.toBeginningOfBacklog": "To beginning of backlog",
			"collections.sendToEndOfBacklog": "Send to end of backlog",
			"collections.assignToIteration": "Assign to iteration…",
		};
		return translations[key] ?? key;
	},
	i18n: { locale: "en-US" },
}));

beforeAll(() => {
	if (!Element.prototype.animate) {
		Element.prototype.animate = () => ({
			finished: Promise.resolve(),
			cancel: () => {},
			addEventListener: () => {},
			removeEventListener: () => {},
		});
	}
	if (!Element.prototype.scrollIntoView)
		Element.prototype.scrollIntoView = () => {};
	if (!globalThis.ResizeObserver) {
		globalThis.ResizeObserver = class {
			observe() {}
			unobserve() {}
			disconnect() {}
		};
	}
	if (!globalThis.requestAnimationFrame) {
		globalThis.requestAnimationFrame = (callback) => {
			callback(performance.now());
			return 0;
		};
	}
});

const { default: BacklogItemActions } = await import(
	"./BacklogItemActions.svelte"
);

afterEach(() => {
	cleanup();
	document.body.innerHTML = "";
});

function renderActions(props = {}) {
	const item = {
		id: 42,
		title: "Improve the backlog",
		iteration_id: 7,
	};
	const onMoveToBoundary = vi.fn();
	const onAssignIteration = vi.fn();

	render(BacklogItemActions, {
		props: {
			item,
			iterations: [],
			onMoveToBoundary,
			onAssignIteration,
			...props,
		},
	});

	return { item, onMoveToBoundary, onAssignIteration };
}

describe("BacklogItemActions", () => {
	test("offers both backlog boundary actions from the row menu", async () => {
		const { item, onMoveToBoundary } = renderActions();

		const trigger = screen.getByTestId("backlog-item-menu-42");
		expect(trigger).toHaveAttribute(
			"aria-label",
			"Backlog actions for Improve the backlog",
		);
		expect(trigger).toHaveStyle("color: var(--ds-text-subtle)");

		await fireEvent.click(trigger);
		await fireEvent.click(screen.getByTestId("backlog-move-start-42"));
		expect(onMoveToBoundary).toHaveBeenCalledWith(item, "start");

		await fireEvent.click(trigger);
		await fireEvent.click(screen.getByTestId("backlog-move-end-42"));
		expect(onMoveToBoundary).toHaveBeenLastCalledWith(item, "end");
	});

	test("assigns to an available iteration from the nested menu", async () => {
		const iteration = { id: 9, name: "Sprint 9", status: "planned" };
		const { item, onAssignIteration } = renderActions({
			iterations: [
				{ id: 7, name: "Current sprint", status: "active" },
				iteration,
			],
		});

		await fireEvent.click(screen.getByTestId("backlog-item-menu-42"));
		await fireEvent.click(
			screen.getByTestId("backlog-assign-iteration-menu-42"),
		);
		await fireEvent.click(screen.getByTestId("backlog-assign-iteration-42-9"));

		expect(onAssignIteration).toHaveBeenCalledWith(item, iteration);
		expect(
			screen.queryByTestId("backlog-assign-iteration-42-7"),
		).not.toBeInTheDocument();
	});

	test("omits iteration assignment when no other iteration is available", async () => {
		renderActions({
			iterations: [{ id: 7, name: "Current sprint", status: "active" }],
		});

		await fireEvent.click(screen.getByTestId("backlog-item-menu-42"));

		expect(
			screen.queryByTestId("backlog-assign-iteration-menu-42"),
		).not.toBeInTheDocument();
		expect(screen.getByTestId("backlog-move-start-42")).toBeInTheDocument();
		expect(screen.getByTestId("backlog-move-end-42")).toBeInTheDocument();
	});
});
