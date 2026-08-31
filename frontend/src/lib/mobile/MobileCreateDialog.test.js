import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeAll, beforeEach, describe, expect, test, vi } from 'vitest';

// jsdom lacks the Web Animations API; Svelte 5 transitions call element.animate
// during outro. Stub it so the dialog's transition resolves immediately.
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

// The dialog reads the workspace list from the stores barrel's workspacesStore
// (a Svelte store exposing .regularWorkspaces). Replace it with a controllable
// writable so the workspace picker renders the options we want.
vi.mock('../stores', async () => {
  const { writable } = await import('svelte/store');
  const workspacesStore = writable({
    regularWorkspaces: [
      { id: 1, name: 'Acme', is_personal: false },
      { id: 2, name: 'Other', is_personal: false },
    ],
    // Personal workspace is loaded on-demand; the dialog checks it in personal
    // mode. Pre-populate it so personal-mode tests don't have to await it.
    personalWorkspace: { id: 3, name: 'Personal', is_personal: true },
  });
  return { workspacesStore };
});

// api — spy on items.create so we can assert the parent_id payload.
vi.mock('../api.js', () => ({
  api: {
    items: { create: vi.fn() },
    itemTypes: { getAll: vi.fn() },
    itemTemplates: { getAll: vi.fn().mockResolvedValue([]) },
    customFields: { getAll: vi.fn().mockResolvedValue({ data: [] }) },
    configurationSets: {
      getAll: vi.fn().mockResolvedValue({ configuration_sets: [] }),
      get: vi.fn().mockResolvedValue(null),
    },
    screens: { getFields: vi.fn().mockResolvedValue([]) },
    milestones: { getAll: vi.fn().mockResolvedValue([]) },
    iterations: { getAll: vi.fn().mockResolvedValue([]) },
    time: { projects: { getByWorkspace: vi.fn().mockResolvedValue([]) } },
    labels: {
      getAll: vi.fn().mockResolvedValue([]),
      create: vi.fn(),
      setForItem: vi.fn().mockResolvedValue([]),
    },
  },
}));

// router.navigate — the dialog navigates after a top-level (non-child) create;
// child creates stay put, but stub it anyway so no real navigation happens.
vi.mock('../router.js', () => ({ navigate: vi.fn() }));

import { api } from '../api.js';
import MobileCreateDialog from './MobileCreateDialog.svelte';

afterEach(() => {
  document.body.innerHTML = '';
});

beforeEach(() => {
  api.items.create.mockReset();
  api.itemTypes.getAll.mockReset();
  api.itemTemplates.getAll.mockReset().mockResolvedValue([]);
  api.customFields.getAll.mockReset().mockResolvedValue({ data: [] });
  api.configurationSets.getAll.mockReset().mockResolvedValue({ configuration_sets: [] });
  api.configurationSets.get.mockReset().mockResolvedValue(null);
  api.screens.getFields.mockReset().mockResolvedValue([]);
  api.milestones.getAll.mockReset().mockResolvedValue([]);
  api.iterations.getAll.mockReset().mockResolvedValue([]);
  api.time.projects.getByWorkspace.mockReset().mockResolvedValue([]);
  api.labels.getAll.mockReset().mockResolvedValue([]);
  api.labels.create.mockReset();
  api.labels.setForItem.mockReset().mockResolvedValue([]);
});

const PARENT = { id: 777, title: 'Epic: Mobile parity' };
const SUB_TYPES = [
  { id: 10, name: 'Story', hierarchy_level: 1 },
  { id: 11, name: 'Task', hierarchy_level: 1 },
];

describe('MobileCreateDialog — child creation', () => {
  test('renders child context and locks type picker to the allowed sub-issue types', () => {
    render(MobileCreateDialog, {
      props: {
        isOpen: true,
        parent: PARENT,
        availableItemTypes: SUB_TYPES,
        workspaceId: 1,
      },
    });

    expect(screen.getByTestId('create-parent')).toHaveTextContent('Epic: Mobile parity');
    expect(screen.getByText('New sub-item')).toBeInTheDocument();

    const typeSelect = screen.getByTestId('create-type');
    const options = [...typeSelect.options].map((o) => o.textContent);
    expect(options).toEqual(['Story', 'Task']);

    // Workspace is locked to the parent's workspace (disabled, Acme selected).
    const wsSelect = screen.getByTestId('create-workspace');
    expect(wsSelect).toBeDisabled();
    expect(wsSelect.value).toBe('1');
  });

  test('submit creates the item under the parent with parent_id set', async () => {
    api.items.create.mockResolvedValue({ id: 999, title: 'New sub' });

    const onclose = vi.fn();
    render(MobileCreateDialog, {
      props: {
        isOpen: true,
        parent: PARENT,
        availableItemTypes: SUB_TYPES,
        workspaceId: 1,
        onclose,
      },
    });

    await fireEvent.input(screen.getByTestId('create-title'), {
      target: { value: 'New sub' },
    });

    await fireEvent.click(screen.getByTestId('create-submit'));

    await waitFor(() => {
      expect(api.items.create).toHaveBeenCalledTimes(1);
    });
    expect(api.items.create).toHaveBeenCalledWith(
      expect.objectContaining({
        title: 'New sub',
        workspace_id: 1,
        item_type_id: 10, // first allowed sub-issue type
        parent_id: 777,
      })
    );
    expect(onclose).toHaveBeenCalled();
  });
});

describe('MobileCreateDialog — configured fields (WI-553)', () => {
  test('renders required configured fields and submits their values', async () => {
    api.items.create.mockResolvedValue({ id: 999, title: 'Screened item' });
    api.itemTypes.getAll.mockResolvedValue([{ id: 10, name: 'Story' }]);
    api.configurationSets.getAll.mockResolvedValue({
      configuration_sets: [{ id: 20, is_default: true, workspace_ids: [1] }],
    });
    api.configurationSets.get.mockResolvedValue({ id: 20, create_screen_id: 55 });
    api.customFields.getAll.mockResolvedValue({
      data: [{ id: 7, name: 'Risk', field_type: 'text' }],
    });
    api.screens.getFields.mockResolvedValue([
      { id: 1, field_type: 'system', field_identifier: 'story_points', is_required: true },
      { id: 2, field_type: 'custom', field_identifier: '7', is_required: true },
    ]);

    render(MobileCreateDialog, { props: { isOpen: true } });

    await fireEvent.input(screen.getByTestId('create-title'), {
      target: { value: 'Screened item' },
    });

    const storyPoints = await screen.findByTestId('configured-system-story_points');
    await fireEvent.input(storyPoints.querySelector('input'), { target: { value: '3' } });

    const risk = await screen.findByTestId('configured-custom-7');
    await fireEvent.input(risk.querySelector('input'), { target: { value: 'High' } });

    await fireEvent.click(screen.getByTestId('create-submit'));

    await waitFor(() => expect(api.items.create).toHaveBeenCalledTimes(1));
    expect(api.items.create).toHaveBeenCalledWith(
      expect.objectContaining({
        title: 'Screened item',
        workspace_id: 1,
        item_type_id: 10,
        story_points: 3,
        custom_field_values: { 7: 'High' },
      })
    );
  });
});

describe('MobileCreateDialog — work item templates (WI-538)', () => {
  test('auto-applies a mandatory template body and locks the description', async () => {
    api.itemTypes.getAll.mockResolvedValue([{ id: 10, name: 'Bug' }]);
    api.itemTemplates.getAll.mockResolvedValue([
      { id: 50, name: 'Bug report', mode: 'mandatory', description_body: '## Repro' },
    ]);

    render(MobileCreateDialog, { props: { isOpen: true } });

    const description = await screen.findByTestId('create-description');
    await waitFor(() => expect(description.value).toBe('## Repro'));

    // Mandatory lock chip shown; no selectable picker.
    expect(await screen.findByTestId('template-picker-locked')).toHaveTextContent(
      'Bug report (enforced)'
    );
    expect(screen.queryByTestId('template-picker')).not.toBeInTheDocument();
    // Description is locked against editing.
    expect(description).toHaveAttribute('readonly');
  });

  test('offers selectable templates in a picker and applies the chosen body', async () => {
    api.itemTypes.getAll.mockResolvedValue([{ id: 10, name: 'Task' }]);
    api.itemTemplates.getAll.mockResolvedValue([
      { id: 60, name: 'Standup', mode: 'selectable', description_body: '- did' },
      { id: 61, name: 'Retro', mode: 'selectable', description_body: '- went' },
    ]);

    render(MobileCreateDialog, { props: { isOpen: true } });

    const picker = await screen.findByTestId('template-picker');
    const options = [...picker.options].map((o) => o.textContent);
    expect(options).toEqual(['No template', 'Standup', 'Retro']);

    await fireEvent.change(picker, { target: { value: '61' } });
    expect(screen.getByTestId('create-description').value).toBe('- went');
  });

  test('skips template loading in personal mode', async () => {
    render(MobileCreateDialog, { props: { isOpen: true, mode: 'personal' } });

    // Personal tasks are title-only — no type, no description, no template UI.
    expect(screen.queryByTestId('template-picker')).not.toBeInTheDocument();
    expect(screen.queryByTestId('template-picker-locked')).not.toBeInTheDocument();
    expect(api.itemTemplates.getAll).not.toHaveBeenCalled();
  });
});
