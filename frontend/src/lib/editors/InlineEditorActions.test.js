import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, expect, test, vi } from 'vitest';

vi.mock('../stores/i18n.svelte.js', () => ({ t: (key) => key }));

import InlineEditorActions from './InlineEditorActions.svelte';

afterEach(cleanup);

test('fires the save and cancel callbacks', async () => {
  const onsave = vi.fn();
  const oncancel = vi.fn();

  render(InlineEditorActions, { props: { onsave, oncancel } });

  await fireEvent.click(screen.getByTitle('editors.saveEnter'));
  await fireEvent.click(screen.getByTitle('editors.cancelEscape'));

  expect(onsave).toHaveBeenCalledTimes(1);
  expect(oncancel).toHaveBeenCalledTimes(1);
});

test('replaces the buttons with a spinner while saving', () => {
  render(InlineEditorActions, { props: { saving: true, onsave: vi.fn(), oncancel: vi.fn() } });

  expect(screen.queryByTitle('editors.saveEnter')).toBeNull();
  expect(screen.queryByTitle('editors.cancelEscape')).toBeNull();
});
