import { afterEach, describe, expect, it, vi } from 'vitest';
import { createPluginBridge, MESSAGE_TYPES } from './PluginBridge.js';

function makeIframe() {
	return { contentWindow: { postMessage: vi.fn() } };
}

function dispatch(iframe, message, { origin = window.location.origin, source } = {}) {
	window.dispatchEvent(
		new MessageEvent('message', {
			data: message,
			origin,
			source: source === undefined ? iframe.contentWindow : source,
		})
	);
}

describe('createPluginBridge editor protocol', () => {
	let bridge = null;

	afterEach(() => {
		bridge?.destroy();
		bridge = null;
	});

	it('routes an editor message that echoes the session id', () => {
		const iframe = makeIframe();
		const onEditorSave = vi.fn();
		bridge = createPluginBridge(iframe, { sessionId: 's1', onEditorSave });

		dispatch(iframe, { type: MESSAGE_TYPES.EDITOR_SAVE, sessionId: 's1', markdown: '# hi' });

		expect(onEditorSave).toHaveBeenCalledWith(
			expect.objectContaining({ sessionId: 's1', markdown: '# hi' })
		);
	});

	it('drops an editor message with a mismatched session id', () => {
		const iframe = makeIframe();
		const onEditorSave = vi.fn();
		bridge = createPluginBridge(iframe, { sessionId: 's1', onEditorSave });

		dispatch(iframe, { type: MESSAGE_TYPES.EDITOR_SAVE, sessionId: 'other', markdown: '# hi' });

		expect(onEditorSave).not.toHaveBeenCalled();
	});

	it('accepts the editor ready handshake before the session id is known', () => {
		// Regression: the plugin only learns the session id from host:editorInit,
		// which the host sends in response to this handshake. Gating ready on the
		// session id deadlocked the editor so it never initialized.
		const iframe = makeIframe();
		const onEditorReady = vi.fn();
		bridge = createPluginBridge(iframe, { sessionId: 's1', onEditorReady });

		dispatch(iframe, { type: MESSAGE_TYPES.EDITOR_READY });

		expect(onEditorReady).toHaveBeenCalledTimes(1);
	});

	it('still drops an editor change that omits the session id', () => {
		const iframe = makeIframe();
		const onEditorChange = vi.fn();
		bridge = createPluginBridge(iframe, { sessionId: 's1', onEditorChange });

		dispatch(iframe, { type: MESSAGE_TYPES.EDITOR_CHANGE, markdown: '# hi', dirty: true });

		expect(onEditorChange).not.toHaveBeenCalled();
	});

	it('drops a message from a different window', () => {
		const iframe = makeIframe();
		const onEditorSave = vi.fn();
		bridge = createPluginBridge(iframe, { sessionId: 's1', onEditorSave });

		dispatch(
			iframe,
			{ type: MESSAGE_TYPES.EDITOR_SAVE, sessionId: 's1', markdown: '# hi' },
			{ source: {} }
		);

		expect(onEditorSave).not.toHaveBeenCalled();
	});

	it('drops a message from a different origin', () => {
		const iframe = makeIframe();
		const onEditorSave = vi.fn();
		bridge = createPluginBridge(iframe, { sessionId: 's1', onEditorSave });

		dispatch(
			iframe,
			{ type: MESSAGE_TYPES.EDITOR_SAVE, sessionId: 's1', markdown: '# hi' },
			{ origin: 'https://evil.example' }
		);

		expect(onEditorSave).not.toHaveBeenCalled();
	});

	it('sends editor init with the session id', () => {
		const iframe = makeIframe();
		bridge = createPluginBridge(iframe, { sessionId: 's1' });

		bridge.sendEditorInit({ markdown: '# hi', entityType: 'item' });

		expect(iframe.contentWindow.postMessage).toHaveBeenCalledWith(
			expect.objectContaining({
				type: MESSAGE_TYPES.EDITOR_INIT,
				sessionId: 's1',
				markdown: '# hi',
				entityType: 'item',
			}),
			window.location.origin
		);
	});

	it('still routes the legacy ready and resize messages', () => {
		const iframe = makeIframe();
		const onReady = vi.fn();
		const onResize = vi.fn();
		bridge = createPluginBridge(iframe, { onReady, onResize });

		dispatch(iframe, { type: MESSAGE_TYPES.READY });
		dispatch(iframe, { type: MESSAGE_TYPES.RESIZE, height: 321 });

		expect(onReady).toHaveBeenCalledTimes(1);
		expect(onResize).toHaveBeenCalledWith(321);
	});
});
