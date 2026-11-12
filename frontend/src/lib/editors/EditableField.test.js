import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

import Harness from './EditableFieldHarness.svelte';

afterEach(cleanup);

function renderField(props = {}) {
  const oncommit = vi.fn().mockResolvedValue(undefined);
  render(Harness, { props: { value: 'original', oncommit, ...props } });
  return { oncommit };
}

async function openEditor() {
  await fireEvent.click(screen.getByTestId('display'));
  return screen.getByTestId('editor');
}

describe('EditableField', () => {
  it('renders the display and opens the editor on click', async () => {
    renderField();

    expect(screen.getByTestId('display')).toHaveTextContent('original');
    const editor = await openEditor();
    expect(editor).toHaveValue('original');
  });

  it('commits the draft on blur', async () => {
    const { oncommit } = renderField();
    const editor = await openEditor();

    await fireEvent.input(editor, { target: { value: 'changed' } });
    await fireEvent.blur(editor);

    expect(oncommit).toHaveBeenCalledWith('changed');
    expect(screen.queryByTestId('editor')).not.toBeInTheDocument();
  });

  it('commits the draft on Enter', async () => {
    const { oncommit } = renderField();
    const editor = await openEditor();

    await fireEvent.input(editor, { target: { value: 'changed' } });
    await fireEvent.keyDown(editor, { key: 'Enter' });

    expect(oncommit).toHaveBeenCalledWith('changed');
  });

  it('cancels on Escape without committing', async () => {
    const { oncommit } = renderField();
    const editor = await openEditor();

    await fireEvent.input(editor, { target: { value: 'changed' } });
    await fireEvent.keyDown(editor, { key: 'Escape' });

    expect(oncommit).not.toHaveBeenCalled();
    expect(screen.queryByTestId('editor')).not.toBeInTheDocument();
  });

  it('does not commit an unchanged value', async () => {
    const { oncommit } = renderField();
    const editor = await openEditor();

    await fireEvent.blur(editor);

    expect(oncommit).not.toHaveBeenCalled();
    expect(screen.queryByTestId('editor')).not.toBeInTheDocument();
  });

  it('keeps the editor open with the error when the commit rejects', async () => {
    const oncommit = vi.fn().mockRejectedValue(new Error('nope'));
    render(Harness, { props: { value: 'original', oncommit } });
    const editor = await openEditor();

    await fireEvent.input(editor, { target: { value: 'changed' } });
    await fireEvent.blur(editor);

    expect(screen.getByTestId('editor')).toBeInTheDocument();
    expect(screen.getByTestId('error')).toHaveTextContent('nope');
  });

  it('does not open the editor when not editable', async () => {
    renderField({ editable: false });

    await fireEvent.click(screen.getByTestId('display'));

    expect(screen.queryByTestId('editor')).not.toBeInTheDocument();
  });
});
