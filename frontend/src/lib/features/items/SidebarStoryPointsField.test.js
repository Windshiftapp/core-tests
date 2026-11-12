import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, test, vi } from 'vitest';

vi.mock('../../stores/i18n.svelte.js', () => ({
  t: (key, params = {}) => {
    const translations = {
      'items.storyPoints': 'Story Points',
      'common.none': 'None',
      'items.enterField': `Enter ${params.field}`,
      'items.storyPointsChildRollup': `${params.points} pts rolled up from ${params.count} child item${params.plural}`,
    };
    return translations[key] ?? key;
  },
}));

const { default: SidebarStoryPointsField } = await import('./SidebarStoryPointsField.svelte');

afterEach(() => cleanup());

function renderField(props = {}) {
  const onSave = vi.fn();
  render(SidebarStoryPointsField, {
    props: { value: null, editable: true, onSave, ...props },
  });
  return { onSave };
}

async function startEditing(existing = '') {
  await fireEvent.click(screen.getByRole('button'));
  const input = screen.getByTestId('story-points-input');
  if (existing !== '') {
    await fireEvent.input(input, { target: { value: existing } });
  }
  return input;
}

describe('SidebarStoryPointsField', () => {
  test('rejects negative input without saving and keeps the editor open', async () => {
    const { onSave } = renderField({ value: 5 });

    const input = await startEditing('-3');
    await fireEvent.blur(input);

    expect(onSave).not.toHaveBeenCalled();
    expect(screen.getByTestId('story-points-input')).toBeInTheDocument();
    expect(input).toHaveAttribute('aria-invalid', 'true');
  });

  test('rejects browser-masked number input instead of clearing the value', async () => {
    const { onSave } = renderField({ value: 5 });

    const input = await startEditing();
    Object.defineProperty(input, 'validity', { value: { badInput: true } });
    // Typing invalid text into a number input fires `input` with badInput set.
    await fireEvent.input(input, { target: { value: '' } });
    await fireEvent.blur(input);

    expect(onSave).not.toHaveBeenCalled();
    expect(screen.getByTestId('story-points-input')).toBeInTheDocument();
    expect(input).toHaveAttribute('aria-invalid', 'true');
  });

  test('saves a valid value and closes the editor', async () => {
    const { onSave } = renderField({ value: null });

    const input = await startEditing('3.5');
    await fireEvent.blur(input);

    expect(onSave).toHaveBeenCalledTimes(1);
    expect(onSave).toHaveBeenCalledWith(3.5);
    expect(screen.queryByTestId('story-points-input')).not.toBeInTheDocument();
  });

  test('does not save when the value is unchanged', async () => {
    const { onSave } = renderField({ value: 5 });

    const input = await startEditing('5');
    await fireEvent.blur(input);

    expect(onSave).not.toHaveBeenCalled();
    expect(screen.queryByTestId('story-points-input')).not.toBeInTheDocument();
  });

  test('clears the value with an empty input', async () => {
    const { onSave } = renderField({ value: 5 });

    const input = await startEditing('5');
    await fireEvent.input(input, { target: { value: '' } });
    await fireEvent.blur(input);

    expect(onSave).toHaveBeenCalledTimes(1);
    expect(onSave).toHaveBeenCalledWith(null);
  });

  test('clearing an already empty value does not save', async () => {
    const { onSave } = renderField({ value: null });

    const input = await startEditing();
    await fireEvent.blur(input);

    expect(onSave).not.toHaveBeenCalled();
    expect(screen.queryByTestId('story-points-input')).not.toBeInTheDocument();
  });

  test('escape cancels editing without saving', async () => {
    const { onSave } = renderField({ value: 5 });

    const input = await startEditing('7');
    await fireEvent.keyDown(input, { key: 'Escape' });

    expect(onSave).not.toHaveBeenCalled();
    expect(screen.queryByTestId('story-points-input')).not.toBeInTheDocument();
  });

  test('shows the child rollup hint when children hold points', () => {
    renderField({ value: null, rollup: { points: 10, contributors: 3 } });

    expect(screen.getByTestId('story-points-child-rollup')).toHaveTextContent(
      '10 pts rolled up from 3 child items'
    );
  });

  test('hides the rollup hint when no child holds points', () => {
    renderField({ value: null, rollup: { points: 0, contributors: 0 } });

    expect(screen.queryByTestId('story-points-child-rollup')).not.toBeInTheDocument();
  });

  test('does not open the editor when the field is not editable', async () => {
    renderField({ editable: false });

    expect(screen.getByRole('button')).toBeDisabled();
    await fireEvent.click(screen.getByRole('button'));

    expect(screen.queryByTestId('story-points-input')).not.toBeInTheDocument();
  });
});
