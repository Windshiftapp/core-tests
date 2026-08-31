import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeAll, describe, expect, test, vi } from 'vitest';

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

import IterationModal from './IterationModal.svelte';

beforeAll(() => {
  if (!Element.prototype.animate) {
    Element.prototype.animate = () => ({
      finished: Promise.resolve(),
      cancel: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      play: () => {},
      pause: () => {},
    });
  }
});

afterEach(() => {
  document.body.innerHTML = '';
});

const validIteration = {
  id: 42,
  name: 'Iteration 42',
  description: '',
  start_date: '2026-07-01T00:00:00Z',
  end_date: '2026-07-14T00:00:00Z',
  status: 'planned',
  type_id: 7,
  is_global: false,
  workspace_id: 3,
};

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

function renderModal(onsave, props = {}) {
  return render(IterationModal, {
    props: {
      iteration: validIteration,
      workspaceId: 3,
      iterationTypes: [{ id: 7, name: 'Sprint' }],
      onsave,
      oncancel: vi.fn(),
      ...props,
    },
  });
}

describe('IterationModal planning scope', () => {
  test.each([
    ['local', { ...validIteration, is_global: false, workspace_id: 3 }],
    ['global', { ...validIteration, is_global: true, workspace_id: null }],
  ])('does not offer %s-to-opposite-scope conversion while editing', (_, iteration) => {
    renderModal(vi.fn(), { iteration, canManageGlobal: true });

    expect(screen.queryByText('iterations.switchTo')).not.toBeInTheDocument();
  });

  test('still offers scope selection while creating', () => {
    renderModal(vi.fn(), { iteration: null, canManageGlobal: true });

    expect(screen.getByText('iterations.switchTo')).toBeInTheDocument();
  });
});

describe('IterationModal async saves', () => {
  test('keeps saving state until delayed success and suppresses repeated submits', async () => {
    const save = deferred();
    const onsave = vi.fn(() => save.promise);
    renderModal(onsave);

    const confirm = screen.getByTestId('dialog-confirm');
    await Promise.all([fireEvent.click(confirm), fireEvent.click(confirm)]);

    expect(onsave).toHaveBeenCalledTimes(1);
    expect(confirm).toBeDisabled();

    save.resolve();

    await waitFor(() => expect(confirm).not.toBeDisabled());
    expect(screen.queryByText('iterations.failedToSaveIteration')).not.toBeInTheDocument();
  });

  test('shows a rejection, accepts a correction, and retries successfully', async () => {
    const firstSave = deferred();
    const secondSave = deferred();
    const onsave = vi
      .fn()
      .mockImplementationOnce(() => firstSave.promise)
      .mockImplementationOnce(() => secondSave.promise);
    renderModal(onsave);

    const confirm = screen.getByTestId('dialog-confirm');
    await fireEvent.click(confirm);
    firstSave.reject(new Error('Iteration save failed'));

    expect(await screen.findByText('Iteration save failed')).toBeInTheDocument();
    await waitFor(() => expect(confirm).not.toBeDisabled());

    const nameInput = document.querySelector('#iteration-name-input');
    await fireEvent.input(nameInput, {
      target: { value: 'Corrected iteration' },
    });
    await fireEvent.click(confirm);

    expect(onsave).toHaveBeenCalledTimes(2);
    expect(onsave).toHaveBeenLastCalledWith(
      expect.objectContaining({ name: 'Corrected iteration' })
    );
    expect(screen.queryByText('Iteration save failed')).not.toBeInTheDocument();
    expect(confirm).toBeDisabled();

    secondSave.resolve();
    await waitFor(() => expect(confirm).not.toBeDisabled());
  });
});
