import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeAll, beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('../../api.js', () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}));

vi.mock('../../router.js', () => ({ navigate: vi.fn() }));
vi.mock('../../stores/i18n.svelte.js', () => ({ t: vi.fn((key) => key) }));
vi.mock('../../stores/toasts.svelte.js', () => ({ errorToast: vi.fn() }));
vi.mock('../../composables/useConfirm.js', () => ({ confirm: vi.fn() }));

import { api } from '../../api.js';
import WorkflowBuilder from './WorkflowBuilder.svelte';

beforeAll(() => {
  if (!Element.prototype.animate) {
    Element.prototype.animate = () => ({
      finished: Promise.resolve(),
      cancel: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
    });
  }
});

beforeEach(() => {
  vi.clearAllMocks();
  api.get.mockImplementation((path) => {
    if (path === '/statuses') return Promise.resolve([{ id: 1, name: 'Open' }]);
    if (path === '/workflows') {
      return Promise.resolve([
        {
          id: 7,
          name: 'Company Workflow',
          description: 'Workflow for the company project',
          is_default: false,
        },
      ]);
    }
    return Promise.resolve([]);
  });
  api.put.mockResolvedValue({
    id: 7,
    name: 'Updated Workflow',
    description: 'Workflow for the company project',
    is_default: false,
  });
});

describe('WorkflowBuilder modal shortcuts', () => {
  test('Cmd/Ctrl+Enter saves an edited workflow from the description textarea', async () => {
    render(WorkflowBuilder);

    await screen.findByText('Company Workflow');
    const actionTrigger = document.querySelector('.dropdown-trigger button');
    expect(actionTrigger).not.toBeNull();
    await fireEvent.click(actionTrigger);
    await fireEvent.click(await screen.findByText('common.edit'));

    const dialog = document.querySelector('[role="dialog"]');
    expect(dialog).not.toBeNull();
    const nameInput = dialog.querySelector('input[type="text"]');
    const description = dialog.querySelector('textarea');
    await fireEvent.input(nameInput, { target: { value: 'Updated Workflow' } });
    await fireEvent.keyDown(description, { key: 'Enter', metaKey: true });

    await waitFor(() => {
      expect(api.put).toHaveBeenCalledWith('/workflows/7', {
        name: 'Updated Workflow',
        description: 'Workflow for the company project',
        is_default: false,
      });
    });
  });
});
