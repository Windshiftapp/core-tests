import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { createRawSnippet } from 'svelte';

vi.mock('../stores/i18n.svelte.js', () => ({ t: (key) => key }));

import SetDetailShell from './SetDetailShell.svelte';

const children = createRawSnippet(() => ({
  render: () => '<div data-testid="shell-content">body</div>',
}));

function baseProps(overrides = {}) {
  return {
    loading: false,
    title: 'Set A',
    description: 'Set description',
    backTooltip: 'Back to sets',
    onBack: vi.fn(),
    hasUnsavedChanges: false,
    saving: false,
    onSave: vi.fn(),
    children,
    ...overrides,
  };
}

afterEach(cleanup);

describe('SetDetailShell', () => {
  test('renders the header and the content snippet once loaded', () => {
    render(SetDetailShell, { props: baseProps() });

    expect(screen.getByText('Set A')).toBeInTheDocument();
    expect(screen.getByText('Set description')).toBeInTheDocument();
    expect(screen.getByTestId('shell-content')).toBeInTheDocument();
  });

  test('shows the loading label instead of the content while loading', () => {
    render(SetDetailShell, { props: baseProps({ loading: true, loadingLabel: 'loading…' }) });

    expect(screen.getByText('loading…')).toBeInTheDocument();
    expect(screen.queryByTestId('shell-content')).toBeNull();
  });

  test('navigates back from the header control', async () => {
    const onBack = vi.fn();
    render(SetDetailShell, { props: baseProps({ onBack }) });

    await fireEvent.click(screen.getByRole('button', { name: 'Back to sets' }));
    expect(onBack).toHaveBeenCalledTimes(1);
  });
});
