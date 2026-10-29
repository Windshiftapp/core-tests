import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import TimeLogModal from './TimeLogModal.svelte';
import { links } from '../api/configuration.js';

const { search } = vi.hoisted(() => ({ search: vi.fn() }));
vi.mock('../api/configuration.js', () => ({
  links: { search },
}));

const restoreDOM = [];
beforeEach(() => {
  search.mockReset();
  search.mockResolvedValue([]);
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
  );
  for (const [name, value] of Object.entries({
    scrollIntoView: () => {},
    animate: () => ({ finished: Promise.resolve(), cancel() {} }),
  })) {
    const original = Object.getOwnPropertyDescriptor(Element.prototype, name);
    Object.defineProperty(Element.prototype, name, { configurable: true, value });
    restoreDOM.push(() => {
      if (original) Object.defineProperty(Element.prototype, name, original);
      else delete Element.prototype[name];
    });
  }
});
afterEach(() => {
  cleanup();
  restoreDOM.splice(0).forEach((restore) => restore());
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const remoteItem = { id: 501, title: 'Remote work item', workspace_name: 'Other workspace' };
const initialItem = { id: 1, title: 'Recent item', workspace_name: 'Home' };

async function openPicker() {
  render(TimeLogModal, { props: { showProjectField: false, workItems: [initialItem] } });
  const input = screen.getByRole('combobox');
  await fireEvent.click(input);
  return input;
}

test('searches items outside the initial list by key and selects their title', async () => {
  search.mockResolvedValue([remoteItem]);
  const input = await openPicker();
  await fireEvent.input(input, { target: { value: 'OTHER-42' } });
  await waitFor(() => expect(search).toHaveBeenCalledWith('OTHER-42', 'item', 20));
  await fireEvent.click(await screen.findByText(remoteItem.title));
  expect(input).toHaveValue(remoteItem.title);
  expect(document.getElementById('time-log-description')).toHaveValue(remoteItem.title);
});

test('clearing a search restores the initial items and ignores a late response', async () => {
  let resolveSearch;
  search.mockReturnValue(
    new Promise((resolve) => {
      resolveSearch = resolve;
    })
  );
  const input = await openPicker();
  await fireEvent.input(input, { target: { value: 'missing' } });
  await waitFor(() => expect(search).toHaveBeenCalledWith('missing', 'item', 20));
  await fireEvent.input(input, { target: { value: '' } });
  await screen.findByText(initialItem.title);
  await act(() => resolveSearch([remoteItem]));
  await waitFor(() => expect(screen.queryByText(remoteItem.title)).not.toBeInTheDocument());
  expect(screen.getByText(initialItem.title)).toBeInTheDocument();
});

test('a failed search clears results and a later search can succeed', async () => {
  vi.spyOn(console, 'error').mockImplementation(() => {});
  search.mockRejectedValueOnce(new Error('Search unavailable')).mockResolvedValue([remoteItem]);
  const input = await openPicker();
  await fireEvent.input(input, { target: { value: 'failed' } });
  await waitFor(() => expect(search).toHaveBeenCalledWith('failed', 'item', 20));
  await waitFor(() => expect(screen.queryByText(initialItem.title)).not.toBeInTheDocument());
  await fireEvent.input(input, { target: { value: 'Remote' } });
  expect(await screen.findByText(remoteItem.title)).toBeInTheDocument();
});

function renderLinkedEditModal(onsave = vi.fn()) {
  render(TimeLogModal, {
    props: {
      projects: [{ id: 7, name: 'Project', status: 'Active' }],
      showProjectField: false,
      workItems: [{ id: 501, title: 'Linked item', workspace_name: 'WS' }],
      editingWorklog: {
        id: 1,
        project_id: 7,
        item_id: 501,
        description: 'Work',
        duration_minutes: 60,
        start_time: 1757000000,
        end_time: 1757003600,
      },
      onsave,
    },
  });
  return onsave;
}

test('clearing the work-item link saves an explicit null item_id', async () => {
  const onsave = renderLinkedEditModal();

  await waitFor(() => expect(document.querySelector('.picker-clear')).not.toBeNull());
  await fireEvent.click(document.querySelector('.picker-clear'));
  await fireEvent.click(screen.getByTestId('dialog-confirm'));

  expect(onsave).toHaveBeenCalledTimes(1);
  expect(onsave.mock.calls[0][0].detail.item_id).toBeNull();
});

test('an edit that leaves the work-item link unchanged preserves it', async () => {
  const onsave = renderLinkedEditModal();

  await fireEvent.input(document.getElementById('time-log-description'), {
    target: { value: 'Updated work' },
  });
  await fireEvent.click(screen.getByTestId('dialog-confirm'));

  expect(onsave).toHaveBeenCalledTimes(1);
  expect(onsave.mock.calls[0][0].detail.item_id).toBe(501);
});

test('an unlinked create saves a null item_id', async () => {
  const onsave = vi.fn();
  render(TimeLogModal, {
    props: {
      projects: [{ id: 7, name: 'Project', status: 'Active' }],
      defaultProjectId: 7,
      showProjectField: false,
      onsave,
    },
  });

  await fireEvent.input(document.getElementById('time-log-description'), {
    target: { value: 'Work' },
  });
  await fireEvent.input(document.getElementById('time-log-duration'), { target: { value: '1h' } });
  await fireEvent.click(screen.getByTestId('dialog-confirm'));

  expect(onsave).toHaveBeenCalledTimes(1);
  expect(onsave.mock.calls[0][0].detail.item_id).toBeNull();
});
