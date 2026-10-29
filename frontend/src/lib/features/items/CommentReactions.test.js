import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
	addCommentReaction: vi.fn(),
	removeCommentReaction: vi.fn(),
}));

vi.mock('../../api/misc.js', () => ({
  addCommentReaction: mocks.addCommentReaction,
  removeCommentReaction: mocks.removeCommentReaction,
}));

vi.mock('../../stores/auth.svelte.js', () => ({
  authStore: {
    currentUser: { id: 7, first_name: 'Test', last_name: 'User', username: 'tester' },
  },
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

import CommentReactionsHarness from './CommentReactionsHarness.svelte';

function chipFor(reaction) {
  return screen.getAllByTestId('comment-reaction').find((chip) => chip.getAttribute('data-reaction') === reaction);
}

describe('CommentReactions', () => {
	it('renders chips in server order and highlights the viewer own reaction', () => {
		render(CommentReactionsHarness, {
			props: {
				initialReactions: [
					{ reaction: 'thumbs_up', count: 2, user_ids: [7, 8], user_names: ['Test User', 'Other'] },
					{ reaction: 'eyes', count: 1, user_ids: [8], user_names: ['Other'] },
				],
			},
		});

		const chips = screen.getAllByTestId('comment-reaction');
		expect(chips).toHaveLength(2);
		expect(chipFor('thumbs_up').getAttribute('aria-pressed')).toBe('true');
		expect(chipFor('thumbs_up')).toHaveTextContent('2');
		expect(chipFor('eyes').getAttribute('aria-pressed')).toBe('false');
		expect(chipFor('eyes')).toHaveTextContent('1');
	});

	it('adds a reaction and applies the grouped reactions the server returns', async () => {
		mocks.addCommentReaction.mockResolvedValue([
			{ reaction: 'thumbs_up', count: 1, user_ids: [7], user_names: ['Test User'] },
			{ reaction: 'eyes', count: 2, user_ids: [8, 7], user_names: ['Other', 'Test User'] },
		]);

		render(CommentReactionsHarness, {
			props: {
				initialReactions: [{ reaction: 'eyes', count: 1, user_ids: [8], user_names: ['Other'] }],
			},
		});

		await fireEvent.click(chipFor('eyes'));
		expect(mocks.addCommentReaction).toHaveBeenCalledWith(11, 'eyes');

		await waitFor(() => expect(chipFor('thumbs_up').getAttribute('aria-pressed')).toBe('true'));
		expect(screen.getAllByTestId('comment-reaction')).toHaveLength(2);
		expect(chipFor('eyes')).toHaveTextContent('2');
	});

	it('removes the viewer own reaction and drops the chip when it was the only one', async () => {
		mocks.removeCommentReaction.mockResolvedValue(undefined);

		render(CommentReactionsHarness, {
			props: {
				initialReactions: [{ reaction: 'thumbs_up', count: 1, user_ids: [7], user_names: ['Test User'] }],
			},
		});

		await fireEvent.click(chipFor('thumbs_up'));
		expect(mocks.removeCommentReaction).toHaveBeenCalledWith(11, 'thumbs_up');

		await waitFor(() => expect(screen.queryByTestId('comment-reaction')).not.toBeInTheDocument());
		expect(mocks.addCommentReaction).not.toHaveBeenCalled();
	});

	it('offers the fixed reaction set in the picker and adds the selected key', async () => {
		mocks.addCommentReaction.mockResolvedValue([
			{ reaction: 'hooray', count: 1, user_ids: [7], user_names: ['Test User'] },
		]);

		render(CommentReactionsHarness, { props: { initialReactions: [] } });

		expect(screen.queryByTestId('comment-reaction-picker')).not.toBeInTheDocument();
		await fireEvent.click(screen.getByTestId('comment-reaction-add'));

		const options = screen.getAllByTestId('comment-reaction-option');
		expect(options.map((option) => option.getAttribute('data-reaction'))).toEqual([
			'thumbs_up', 'check', 'eyes', 'heart', 'laugh', 'hooray',
		]);

		await fireEvent.click(options.find((option) => option.getAttribute('data-reaction') === 'hooray'));
		expect(mocks.addCommentReaction).toHaveBeenCalledWith(11, 'hooray');
		await waitFor(() => expect(chipFor('hooray')).toBeInTheDocument());
	});

	it('hides the add control and disables chips without comment permission', () => {
		render(CommentReactionsHarness, {
			props: {
				canReact: false,
				initialReactions: [{ reaction: 'thumbs_up', count: 1, user_ids: [8], user_names: ['Other'] }],
			},
		});

		expect(screen.queryByTestId('comment-reaction-add')).not.toBeInTheDocument();
		expect(chipFor('thumbs_up')).toBeDisabled();
	});

	it('reverts the optimistic toggle and reports the failure when the request fails', async () => {
		mocks.addCommentReaction.mockRejectedValue(new Error('network down'));
		const onError = vi.fn();

		render(CommentReactionsHarness, {
			props: {
				onError,
				initialReactions: [{ reaction: 'eyes', count: 1, user_ids: [8], user_names: ['Other'] }],
			},
		});

		await fireEvent.click(chipFor('eyes'));

		await waitFor(() => expect(onError).toHaveBeenCalledWith('comments.reactionFailed'));
		expect(chipFor('eyes')).toHaveTextContent('1');
		expect(chipFor('eyes').getAttribute('aria-pressed')).toBe('false');
	});
});
