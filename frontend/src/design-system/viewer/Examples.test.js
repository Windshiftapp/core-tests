import { fireEvent, render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import BoardExample from './pages/BoardExample.svelte';
import FormExample from './pages/FormExample.svelte';

vi.mock('../../lib/stores/i18n.svelte.js', () => ({
  t: vi.fn((key) => key),
}));

beforeEach(() => {
  document.body.innerHTML = '';
});

describe('design system composition examples', () => {
  it('filters the board by search text and priority', async () => {
    render(BoardExample);

    expect(screen.getByText('Polish the new workspace onboarding')).toBeInTheDocument();
    expect(screen.getByText('Publish Q3 launch checklist')).toBeInTheDocument();

    await fireEvent.input(screen.getByTestId('design-system-board-search'), {
      target: { value: 'accessibility' },
    });

    expect(screen.getByText('Review accessibility of primary flows')).toBeInTheDocument();
    expect(screen.queryByText('Publish Q3 launch checklist')).not.toBeInTheDocument();

    await fireEvent.input(screen.getByTestId('design-system-board-search'), {
      target: { value: '' },
    });
    await fireEvent.click(screen.getByTestId('design-system-board-priority-filter'));

    expect(screen.getByText('Polish the new workspace onboarding')).toBeInTheDocument();
    expect(screen.queryByText('Publish Q3 launch checklist')).not.toBeInTheDocument();
  });

  it('renders the production creation form examples with representative data', async () => {
    render(FormExample);

    const submitButton = screen.getByTestId('design-system-form-submit');

    expect(screen.getByTestId('design-system-create-form')).toBeInTheDocument();
    expect(screen.getByTestId('design-system-form-pattern-request')).toBeInTheDocument();
    expect(screen.getByTestId('design-system-form-pattern-configuration')).toBeInTheDocument();
    expect(screen.getByTestId('design-system-form-pattern-quick-add')).toBeInTheDocument();
    expect(document.getElementById('work-item-title')).toHaveValue(
      'Prepare the release readiness review'
    );
    expect(screen.getByTestId('design-system-form-workspace')).toHaveTextContent('WIND');

    await fireEvent.click(submitButton);

    expect(
      screen.getByText('The work item example is valid. Nothing was saved.')
    ).toBeInTheDocument();
  });
});
