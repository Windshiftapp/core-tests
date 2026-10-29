import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { writable } from 'svelte/store';
import { beforeEach, describe, expect, test, vi } from 'vitest';

vi.mock('../../api/core.js', () => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  delete: vi.fn(),
}))

vi.mock('../../api/workflows.js', () => ({
  statuses: { getAll: vi.fn() },
  workflows: {
        create: vi.fn(),
        delete: vi.fn(),
        getAll: vi.fn(),
        update: vi.fn(),
      },
}))

vi.mock('../../api/objectTranslations.js', () => ({
  objectTranslations: {
        list: vi.fn(),
        resolve: vi.fn(),
        upsert: vi.fn(),
        delete: vi.fn(),
      },
}));

vi.mock('../../router.js', () => ({ navigate: vi.fn() }));
vi.mock('../../stores/i18n.svelte.js', () => ({
  i18n: {
    locale: 'en',
    supportedLocales: [{ code: 'en', name: 'English', direction: 'ltr' }],
  },
  t: vi.fn((key) => key),
}));
vi.mock('../../stores/permissions.svelte.js', () => ({
  isSystemAdmin: writable(true),
}));
vi.mock('../../stores/toasts.svelte.js', () => ({ errorToast: vi.fn() }));
vi.mock('../../composables/useConfirm.js', () => ({ confirm: vi.fn() }));

import { objectTranslations } from '../../api/objectTranslations.js';
import { statuses, workflows } from '../../api/workflows.js';
import WorkflowBuilder from './WorkflowBuilder.svelte';
import { get, post, put } from '../../api/core.js';

beforeEach(() => {
  vi.clearAllMocks();
  objectTranslations.list.mockResolvedValue([]);
  objectTranslations.upsert.mockResolvedValue({});
  statuses.getAll.mockResolvedValue([{ id: 1, name: 'Open' }]);
  workflows.getAll.mockResolvedValue([
    {
      id: 7,
      name: 'Company Workflow',
      description: 'Workflow for the company project',
      is_default: false,
    },
  ]);
  workflows.update.mockResolvedValue({
    id: 7,
    name: 'Updated Workflow',
    description: 'Workflow for the company project',
    is_default: false,
  });
});

describe('WorkflowBuilder modal shortcuts', () => {
  test('submits a new workflow only once while the first create is pending', async () => {
    let resolveCreate;
    workflows.create.mockReturnValue(
      new Promise((resolve) => {
        resolveCreate = resolve;
      })
    );
    render(WorkflowBuilder);

    await screen.findByText('Company Workflow');
    await fireEvent.click(screen.getByText('workflows.createWorkflow'));
    const nameInput = screen.getByPlaceholderText('workflows.workflowNamePlaceholder');
    await fireEvent.input(nameInput, { target: { value: 'One workflow' } });
    const form = nameInput.closest('form');

    await fireEvent.submit(form);
    await fireEvent.submit(form);

    expect(workflows.create).toHaveBeenCalledTimes(1);
    resolveCreate({ id: 8, name: 'One workflow', description: '', is_default: false });
    await Promise.resolve();
  });

  test('Cmd/Ctrl+Enter saves an edited workflow from the description textarea', async () => {
    render(WorkflowBuilder);

    await screen.findByText('Company Workflow');
    const actionTrigger = document.querySelector('.dropdown-trigger button');
    expect(actionTrigger).not.toBeNull();
    await fireEvent.click(actionTrigger);
    await fireEvent.click(await screen.findByText('common.edit'));

    const dialog = document.querySelector('[role="dialog"]');
    expect(dialog).not.toBeNull();
    const nameInput = screen.getByTestId('localized-object-name-en');
    const description = screen.getByTestId('localized-object-description-en');
    await fireEvent.input(nameInput, { target: { value: 'Updated Workflow' } });
    await fireEvent.keyDown(description, { key: 'Enter', metaKey: true });

    await waitFor(() => {
      expect(workflows.update).toHaveBeenCalledWith(7, {
        name: 'Company Workflow',
        description: 'Workflow for the company project',
        is_default: false,
      });
      expect(objectTranslations.upsert).toHaveBeenCalledWith(
        'workflow',
        7,
        'name',
        'en',
        'Updated Workflow'
      );
    });
  });

  test('submits an edited workflow only once while the first save is pending', async () => {
    let resolveUpdate;
    workflows.update.mockReturnValue(
      new Promise((resolve) => {
        resolveUpdate = resolve;
      })
    );
    render(WorkflowBuilder);

    await screen.findByText('Company Workflow');
    const actionTrigger = document.querySelector('.dropdown-trigger button');
    await fireEvent.click(actionTrigger);
    await fireEvent.click(await screen.findByText('common.edit'));

    const description = screen.getByTestId('localized-object-description-en');
    await fireEvent.keyDown(description, { key: 'Enter', metaKey: true });
    await fireEvent.keyDown(description, { key: 'Enter', metaKey: true });

    expect(workflows.update).toHaveBeenCalledTimes(1);
    resolveUpdate({ id: 7 });
    await waitFor(() => expect(workflows.getAll).toHaveBeenCalledTimes(2));
  });
});
