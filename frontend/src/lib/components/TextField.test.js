import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, expect, test, vi } from 'vitest';
import { createRawSnippet } from 'svelte';

vi.mock('../stores/i18n.svelte.js', () => ({ t: (key) => key }));

import TextField from './TextField.svelte';

afterEach(cleanup);

test('renders the label and the bound input value', () => {
  render(TextField, { props: { label: 'Name', id: 'name', value: 'Ada' } });

  expect(screen.getByText('Name')).toBeInTheDocument();
  expect(screen.getByLabelText(/Name/)).toHaveValue('Ada');
});

test('marks the field required without requiring the input', () => {
  render(TextField, { props: { label: 'Name', id: 'name', requiredMark: true } });

  expect(screen.getByText(/Name/).textContent).toContain('*');
  expect(screen.getByLabelText(/Name/)).not.toBeRequired();
});

test('sets the required attribute when required', () => {
  render(TextField, { props: { label: 'Name', id: 'name', required: true } });

  expect(screen.getByLabelText(/Name/)).toBeRequired();
});

test('renders rich label content through labelSnippet', () => {
  const labelSnippet = createRawSnippet(() => ({
    render: () => '<em data-testid="rich-label">Rich</em>',
  }));

  render(TextField, { props: { labelSnippet, id: 'name' } });

  expect(screen.getByTestId('rich-label')).toBeInTheDocument();
  expect(screen.getByLabelText(/Rich/)).toBeInTheDocument();
});
