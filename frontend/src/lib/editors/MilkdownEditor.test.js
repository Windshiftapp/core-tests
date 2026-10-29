import { render, waitFor } from "@testing-library/svelte";
import { describe, expect, it, vi } from "vitest";
import { attachments } from '../api/misc.js';

// The Milkdown builder is async: `create()` resolves after the component may
// already have unmounted. Drive it with a deferred promise and capture the
// listener callbacks so teardown behavior can be asserted.
const h = vi.hoisted(() => {
	const state = { chain: null, deferred: null };
	const listeners = { markdownUpdated: null, updated: null };
	const ctx = {
		set: () => {},
		update: () => {},
		get: () => ({
			markdownUpdated: (cb) => {
				listeners.markdownUpdated = cb;
			},
			updated: (cb) => {
				listeners.updated = cb;
			},
		}),
	};

	function makeChain() {
		const configs = [];
		const chain = {};
		chain.config = (fn) => {
			configs.push(fn);
			return chain;
		};
		chain.use = () => chain;
		chain.create = () => {
			// `.config()` accepts both callbacks and plugin objects (e.g. nord).
			for (const fn of configs) {
				if (typeof fn === "function") fn(ctx);
			}
			state.deferred = {};
			state.deferred.promise = new Promise((resolve) => {
				state.deferred.resolve = resolve;
			});
			return state.deferred.promise;
		};
		state.chain = chain;
		return chain;
	}

	return { state, listeners, ctx, makeChain };
});

vi.mock("@milkdown/kit/core", () => ({
	Editor: { make: () => h.makeChain() },
	rootCtx: Symbol("root"),
	defaultValueCtx: Symbol("defaultValue"),
	editorViewOptionsCtx: Symbol("editorViewOptions"),
	editorViewCtx: Symbol("editorView"),
	serializerCtx: Symbol("serializer"),
	remarkPluginsCtx: Symbol("remarkPlugins"),
}));
vi.mock("@milkdown/kit/preset/commonmark", () => ({
	commonmark: {},
	toggleStrongCommand: { key: "strong" },
	toggleEmphasisCommand: { key: "emphasis" },
	wrapInBulletListCommand: { key: "bullet" },
	wrapInOrderedListCommand: { key: "ordered" },
	toggleInlineCodeCommand: { key: "inlineCode" },
}));
vi.mock("@milkdown/kit/preset/gfm", () => ({
	gfm: {},
	toggleStrikethroughCommand: { key: "strikethrough" },
}));
vi.mock("@milkdown/kit/plugin/listener", () => ({ listener: {}, listenerCtx: Symbol("listener") }));
vi.mock("@milkdown/kit/plugin/clipboard", () => ({ clipboard: {} }));
vi.mock("@milkdown/kit/plugin/upload", () => ({ upload: {}, uploadConfig: { key: Symbol("upload") } }));
vi.mock("@milkdown/kit/utils", () => ({ insert: () => {}, callCommand: () => () => {} }));
vi.mock("@milkdown/utils", () => ({ replaceAll: () => {} }));
vi.mock("@milkdown/theme-nord", () => ({ nord: {} }));
vi.mock("@milkdown/kit/component/image-block", () => ({ imageBlockComponent: {} }));
vi.mock('../api/misc.js', () => ({
  attachments: {},
}));
vi.mock("../composables/useConfirm.js", () => ({ confirm: vi.fn() }));
vi.mock("../stores/i18n.svelte.js", () => ({ t: (key) => key }));
vi.mock("../stores/attachmentStatus.svelte.js", () => ({
	attachmentStatus: { loaded: true, enabled: true },
}));
vi.mock("../router.js", () => ({ navigate: vi.fn() }));
vi.mock("./milkdown-mention-mark.js", () => ({ mentionDecorationPlugin: {} }));
vi.mock("./milkdown-link-sanitizer.js", () => ({ linkSanitizerPlugin: {} }));
vi.mock("./milkdown-hardbreak.js", () => ({ rewriteBreakHTML: {} }));
vi.mock("./milkdown-excalidraw-block.svelte.js", () => ({ excalidrawBlock: {} }));
vi.mock("./code-highlight.js", () => ({ highlightCodeBlocks: () => {} }));

const { default: MilkdownEditor } = await import("./MilkdownEditor.svelte");

function fakeEditor() {
	return { destroy: vi.fn().mockResolvedValue(undefined), action: vi.fn() };
}

describe("MilkdownEditor async creation lifecycle (WI-1613)", () => {
	it("destroys an editor that finishes creating after unmount exactly once", async () => {
		const editor = fakeEditor();
		const { unmount } = render(MilkdownEditor, { props: { content: "hello" } });
		await waitFor(() => expect(h.state.deferred).not.toBeNull());

		// Unmount while builder.create() is still pending.
		unmount();
		h.state.deferred.resolve(editor);

		await waitFor(() => expect(editor.destroy).toHaveBeenCalledTimes(1));
	});

	it("destroys a normally created editor exactly once on unmount", async () => {
		const editor = fakeEditor();
		const { unmount } = render(MilkdownEditor, { props: { content: "hello" } });
		await waitFor(() => expect(h.state.deferred).not.toBeNull());
		h.state.deferred.resolve(editor);

		await waitFor(() => expect(editor.destroy).not.toHaveBeenCalled());
		unmount();
		await waitFor(() => expect(editor.destroy).toHaveBeenCalledTimes(1));
	});

	it("ignores editor listener callbacks after disposal", async () => {
		const editor = fakeEditor();
		const onContentChange = vi.fn();
		const { unmount } = render(MilkdownEditor, {
			props: { content: "hello", onContentChange },
		});
		await waitFor(() => expect(h.state.deferred).not.toBeNull());
		h.state.deferred.resolve(editor);
		await waitFor(() => expect(h.listeners.markdownUpdated).toBeTypeOf("function"));

		unmount();
		onContentChange.mockClear();
		h.listeners.markdownUpdated(h.ctx, "stale markdown");

		expect(onContentChange).not.toHaveBeenCalled();
		await waitFor(() => expect(editor.destroy).toHaveBeenCalledTimes(1));
	});
});
