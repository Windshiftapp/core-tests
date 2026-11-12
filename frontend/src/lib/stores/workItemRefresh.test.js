import { afterEach, describe, expect, it, vi } from 'vitest';
import {
	onWorkItemRefresh,
	resetWorkItemRefresh,
	workItemRefreshTouchesWorkspace,
} from './workItemRefresh.js';

function dispatch(detail) {
	window.dispatchEvent(new CustomEvent('refresh-work-items', { detail }));
}

afterEach(() => {
	resetWorkItemRefresh();
});

describe('workItemRefresh', () => {
	it('notifies a subscriber once per event with the detail', async () => {
		const seen = [];
		const unsubscribe = onWorkItemRefresh((detail) => seen.push(detail));
		dispatch({ item: { id: 7, workspace_id: 3 }, itemId: 7, parentId: 2 });

		await vi.waitFor(() => expect(seen).toHaveLength(1));
		expect(seen[0].itemId).toBe(7);
		expect(seen[0].item).toEqual({ id: 7, workspace_id: 3 });
		expect(seen[0].parentIds).toEqual([2]);
		unsubscribe();
	});

	it('coalesces a burst into one notification and merges the named ids', async () => {
		const seen = [];
		const unsubscribe = onWorkItemRefresh((detail) => seen.push(detail));
		dispatch({ itemId: 1, parentId: 10 });
		dispatch({ itemId: 2, parentId: 11 });
		dispatch({ itemId: 3, parentId: 10 });

		await vi.waitFor(() => expect(seen).toHaveLength(1));
		expect(seen[0].itemIds).toEqual([1, 2, 3]);
		expect(seen[0].parentIds).toEqual([10, 11]);
		unsubscribe();
	});

	it('stops notifying after unsubscribe', async () => {
		const seen = [];
		const unsubscribe = onWorkItemRefresh((detail) => seen.push(detail));
		unsubscribe();
		vi.useFakeTimers();
		try {
			dispatch({ itemId: 1 });

			await vi.advanceTimersByTimeAsync(5);
			expect(seen).toHaveLength(0);
		} finally {
			vi.useRealTimers();
		}
	});

	it('keeps notifying other subscribers when one throws', async () => {
		const seen = [];
		const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
		const unsubscribeA = onWorkItemRefresh(() => {
			throw new Error('boom');
		});
		const unsubscribeB = onWorkItemRefresh((detail) => seen.push(detail));
		dispatch({ itemId: 9 });

		await vi.waitFor(() => expect(seen).toHaveLength(1));
		expect(seen[0].itemId).toBe(9);
		expect(errorSpy).toHaveBeenCalled();
		unsubscribeA();
		unsubscribeB();
		errorSpy.mockRestore();
	});

	it('drops pending work on reset', async () => {
		const seen = [];
		onWorkItemRefresh((detail) => seen.push(detail));
		vi.useFakeTimers();
		try {
			dispatch({ itemId: 1 });
			resetWorkItemRefresh();

			await vi.advanceTimersByTimeAsync(5);
			expect(seen).toHaveLength(0);
		} finally {
			vi.useRealTimers();
		}
	});
});

describe('workItemRefreshTouchesWorkspace', () => {
	it('matches the workspace named by the burst', () => {
		expect(workItemRefreshTouchesWorkspace({ item: { workspace_id: 3 } }, 3)).toBe(true);
		expect(workItemRefreshTouchesWorkspace({ item: { workspace_id: 3 } }, 4)).toBe(false);
	});

	it('treats a burst without workspace information as touching every workspace', () => {
		expect(workItemRefreshTouchesWorkspace({ itemId: 9 }, 4)).toBe(true);
		expect(workItemRefreshTouchesWorkspace({}, null)).toBe(true);
	});
});
