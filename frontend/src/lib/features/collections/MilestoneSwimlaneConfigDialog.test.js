import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import MilestoneSwimlaneConfigDialog from './MilestoneSwimlaneConfigDialog.svelte';

vi.mock('../../stores/i18n.svelte.js', () => ({ t: (key) => key }));

beforeAll(() => {
	// jsdom lacks the layout APIs Melt's floating menu touches on open.
	if (!Element.prototype.scrollIntoView) {
		Element.prototype.scrollIntoView = () => {};
	}
	if (!globalThis.ResizeObserver) {
		globalThis.ResizeObserver = class {
			observe() {}
			unobserve() {}
			disconnect() {}
		};
	}
});

afterEach(() => {
	cleanup();
	// The picker dropdown is portalled to document.body and outros via a fly
	// transition, so stragglers can outlive the component between renders.
	document.body.innerHTML = '';
});

const globalMilestones = [
	{ id: 1, name: 'Global A', target_date: '2026-01-01', is_global: true },
	{ id: 2, name: 'Global B', target_date: null, is_global: true, status: 'completed' },
];

function renderDialog(props = {}) {
	return render(MilestoneSwimlaneConfigDialog, {
		props: {
			isOpen: true,
			globalMilestones,
			selectedGlobalMilestoneIds: [],
			showCompleted: true,
			onsave: () => {},
			onclose: () => {},
			...props,
		},
	});
}

async function openMilestonePicker() {
	await fireEvent.click(screen.getByTestId('item-picker-trigger'));
	return screen.findByTestId('milestone-swimlanes-search');
}

describe('MilestoneSwimlaneConfigDialog', () => {
	it('prefills the stored preference and emits the changed selection', async () => {
		const onsave = vi.fn();
		renderDialog({ selectedGlobalMilestoneIds: [1], showCompleted: true, onsave });

		expect(
			screen.getByTestId('milestone-swimlanes-show-completed').querySelector('input').checked,
		).toBe(true);

		await openMilestonePicker();
		// The trigger chips are the observable selection state (Melt's
		// aria-selected tracks its own single-select state, not our manual
		// multi-select).
		expect(screen.getByTestId('item-picker-trigger')).toHaveTextContent('Global A');
		expect(screen.getByTestId('item-picker-trigger')).not.toHaveTextContent('Global B');

		await fireEvent.click(await screen.findByTestId('milestone-swimlanes-global-2'));
		expect(screen.getByTestId('item-picker-trigger')).toHaveTextContent('Global B');
		await fireEvent.click(
			screen.getByTestId('milestone-swimlanes-show-completed').querySelector('input'),
		);
		await fireEvent.click(screen.getByTestId('milestone-swimlanes-save'));

		expect(onsave).toHaveBeenCalledWith({
			globalMilestoneIds: [1, 2],
			showCompleted: false,
			showOpenFromCompleted: false,
		});
	});

	it('prefills and emits the open-from-completed rescue toggle', async () => {
		const onsave = vi.fn();
		renderDialog({ showOpenFromCompleted: true, onsave });

		expect(
			screen
				.getByTestId('milestone-swimlanes-show-open-from-completed')
				.querySelector('input').checked,
		).toBe(true);

		await fireEvent.click(
			screen
				.getByTestId('milestone-swimlanes-show-open-from-completed')
				.querySelector('input'),
		);
		await fireEvent.click(screen.getByTestId('milestone-swimlanes-save'));

		expect(onsave).toHaveBeenCalledWith({
			globalMilestoneIds: [],
			showCompleted: true,
			showOpenFromCompleted: false,
		});
	});

	it('filters the global milestone picker by search query', async () => {
		renderDialog();
		const search = await openMilestonePicker();

		await fireEvent.input(search, { target: { value: 'Global B' } });

		await waitFor(() => {
			expect(screen.queryByTestId('milestone-swimlanes-global-1')).not.toBeInTheDocument();
		});
		expect(screen.getByTestId('milestone-swimlanes-global-2')).toBeInTheDocument();
	});

	it('disables save until a preference changes', async () => {
		const onsave = vi.fn();
		renderDialog({ selectedGlobalMilestoneIds: [1], showCompleted: true, onsave });

		expect(screen.getByTestId('milestone-swimlanes-save').disabled).toBe(true);

		await openMilestonePicker();
		await fireEvent.click(await screen.findByTestId('milestone-swimlanes-global-1'));
		expect(screen.getByTestId('milestone-swimlanes-save').disabled).toBe(false);
	});
});
