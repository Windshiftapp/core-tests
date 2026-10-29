import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

const { searchLinkable, mergeInto, split, getComments } = vi.hoisted(() => ({
  searchLinkable: vi.fn(),
  mergeInto: vi.fn(),
  split: vi.fn(),
  getComments: vi.fn(),
}));

vi.mock('../../api/items.js', () => ({
  items: { mergeInto, split },
}))

vi.mock('../../api/configuration.js', () => ({
  links: { search: searchLinkable },
}))

vi.mock('../../api/misc.js', () => ({
  getComments: getComments,
}));

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key, params) =>
    [key, ...(params && typeof params === 'object' ? Object.values(params) : [])].join(' '),
}));

import ItemMergeDialog from './ItemMergeDialog.svelte';
import ItemSplitDialog from './ItemSplitDialog.svelte';
import { items } from '../../api/items.js';
import { links } from '../../api/configuration.js';



const item = { id: 7, workspace_key: 'SUP', workspace_item_number: 42, title: 'Canonical' };

afterEach(() => {
  cleanup();
  document.body.innerHTML = '';
  searchLinkable.mockReset();
  mergeInto.mockReset();
  split.mockReset();
  getComments.mockReset();
});

describe('ItemMergeDialog', () => {
  it('searches for a duplicate and merges the selected ticket', async () => {
    searchLinkable.mockResolvedValue([
      { id: 99, title: 'Duplicate request', workspace_key: 'SUP', workspace_item_number: 99 },
    ]);
    mergeInto.mockResolvedValue({
      sources: [{ source_item_id: 99, target_item_id: 7, merged: true, moved_comments: 1 }],
    });

    let merged = null;
    const onMerged = (result) => { merged = result; };

    render(ItemMergeDialog, { props: { isOpen: true, item, onMerged } });

    await fireEvent.click(screen.getByTestId('item-picker-trigger'));
    const search = await screen.findByTestId('item-merge-duplicate-search');
    await fireEvent.input(search, { target: { value: 'duplicate' } });

    const option = await screen.findByTestId('item-merge-duplicate-option-99');
    await fireEvent.click(option);
    await fireEvent.click(screen.getByTestId('item-merge-confirm'));

    await waitFor(() => expect(searchLinkable).toHaveBeenCalledWith('duplicate', 'item', 20));
    await waitFor(() => expect(mergeInto).toHaveBeenCalledWith(7, [99]));
    await waitFor(() => expect(merged).not.toBeNull());
    expect(merged.sources[0].moved_comments).toBe(1);
  });

  it('merges the candidate passed in by the duplicates panel', async () => {
    mergeInto.mockResolvedValue({ sources: [] });

    render(ItemMergeDialog, {
      props: {
        isOpen: true,
        item,
        preselected: { id: 88, title: 'Candidate', workspace_key: 'SUP', workspace_item_number: 88 },
        onMerged: () => {},
      },
    });

    await fireEvent.click(screen.getByTestId('item-merge-confirm'));

    await waitFor(() => expect(mergeInto).toHaveBeenCalledWith(7, [88]));
  });

  it('surfaces failures instead of closing', async () => {
    mergeInto.mockRejectedValue(new Error('merge conflict'));

    render(ItemMergeDialog, {
      props: {
        isOpen: true,
        item,
        preselected: { id: 88, title: 'Candidate' },
        onMerged: () => {},
      },
    });

    await fireEvent.click(screen.getByTestId('item-merge-confirm'));

    await waitFor(() => expect(screen.getByTestId('item-merge-error')).toBeInTheDocument());
    expect(screen.getByTestId('item-merge-error')).toHaveTextContent('merge conflict');
  });

  it('warns when the duplicate has a different requester', async () => {
    searchLinkable.mockResolvedValue([
      {
        id: 99,
        title: 'Other customer request',
        workspace_key: 'SUP',
        workspace_item_number: 99,
        creator_portal_customer_id: 2,
      },
    ]);

    render(ItemMergeDialog, {
      props: {
        isOpen: true,
        item: { ...item, creator_portal_customer_id: 1 },
        onMerged: () => {},
      },
    });

    await fireEvent.click(screen.getByTestId('item-picker-trigger'));
    const search = await screen.findByTestId('item-merge-duplicate-search');
    await fireEvent.input(search, { target: { value: 'other' } });
    await fireEvent.click(await screen.findByTestId('item-merge-duplicate-option-99'));

    expect(await screen.findByTestId('item-merge-requester-warning')).toBeInTheDocument();
  });

  it('does not warn when both tickets share a requester', async () => {
    searchLinkable.mockResolvedValue([
      {
        id: 99,
        title: 'Same customer request',
        workspace_key: 'SUP',
        workspace_item_number: 99,
        creator_portal_customer_id: 1,
      },
    ]);

    render(ItemMergeDialog, {
      props: {
        isOpen: true,
        item: { ...item, creator_portal_customer_id: 1 },
        onMerged: () => {},
      },
    });

    await fireEvent.click(screen.getByTestId('item-picker-trigger'));
    const search = await screen.findByTestId('item-merge-duplicate-search');
    await fireEvent.input(search, { target: { value: 'same' } });
    await fireEvent.click(await screen.findByTestId('item-merge-duplicate-option-99'));

    expect(screen.queryByTestId('item-merge-requester-warning')).not.toBeInTheDocument();
  });
});

const splitItemTypes = [
  { id: 3, name: 'Task' },
  { id: 5, name: 'Bug' },
];

describe('ItemSplitDialog', () => {
  it('creates the subticket with the selected comments and default child type', async () => {
    getComments.mockResolvedValue({
      comments: [
        { id: 1, content: 'first comment' },
        { id: 2, content: 'second comment' },
      ],
    });
    split.mockResolvedValue({ split_item_id: 55, moved_comments: 1 });

    let splitResult = null;
    const onSplit = (result) => { splitResult = result; };

    render(ItemSplitDialog, { props: { isOpen: true, item, itemTypes: splitItemTypes, onSplit } });

    await waitFor(() => expect(getComments).toHaveBeenCalledWith(7));

    await fireEvent.input(screen.getByTestId('item-split-title-input'), {
      target: { value: 'Follow-up' },
    });
    await fireEvent.click(screen.getByTestId('item-split-comment-2'));
    await fireEvent.click(screen.getByTestId('item-split-confirm'));

    await waitFor(() => expect(split).toHaveBeenCalledWith(7, {
      title: 'Follow-up',
      item_type_id: 3,
      comment_ids: [2],
    }));
    await waitFor(() => expect(splitResult?.split_item_id).toBe(55));
  });

  it('sends a different child type when the user picks one', async () => {
    getComments.mockResolvedValue({ comments: [{ id: 1, content: 'only' }] });
    split.mockResolvedValue({ split_item_id: 56 });

    render(ItemSplitDialog, {
      props: { isOpen: true, item, itemTypes: splitItemTypes, onSplit: () => {} },
    });

    await waitFor(() => expect(screen.getByTestId('item-split-comment-1')).toBeInTheDocument());
    await fireEvent.input(screen.getByTestId('item-split-title-input'), { target: { value: 'Follow-up' } });
    await fireEvent.click(screen.getByTestId('item-split-comment-1'));

    await fireEvent.click(document.getElementById('item-split-type'));
    const bugOption = await waitFor(() => document.querySelector('[data-option-id="5"]'));
    await fireEvent.click(bugOption);

    await fireEvent.click(screen.getByTestId('item-split-confirm'));

    await waitFor(() => expect(split).toHaveBeenCalledWith(7, {
      title: 'Follow-up',
      item_type_id: 5,
      comment_ids: [1],
    }));
  });

  it('requires a selection before confirming', async () => {
    getComments.mockResolvedValue({ comments: [{ id: 1, content: 'only' }] });

    render(ItemSplitDialog, {
      props: { isOpen: true, item, itemTypes: splitItemTypes, onSplit: () => {} },
    });

    await waitFor(() => expect(screen.getByTestId('item-split-comment-1')).toBeInTheDocument());
    await fireEvent.input(screen.getByTestId('item-split-title-input'), { target: { value: 'X' } });

    const confirm = screen.getByTestId('item-split-confirm');
    expect(confirm).toBeDisabled();
    expect(split).not.toHaveBeenCalled();
  });
});
