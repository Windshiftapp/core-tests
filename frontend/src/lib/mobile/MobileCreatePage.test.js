import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';

// during outro. Stub it so any sheet transition resolves immediately.
// The page is route-driven: mode ('work' | 'personal') and parent come from
// the /m/new query string. The router mock publishes its store through the
// hoisted holder so tests can point the route at each mode.
const routerMocks = vi.hoisted(() => ({ currentRoute: undefined }));
vi.mock('../router.js', async () => {
  const { writable } = await import('svelte/store');
  routerMocks.currentRoute = writable({
    path: '/m/new',
    view: 'mobile-create',
    params: {},
    query: {},
  });
  return {
    navigate: vi.fn(),
    setNavigationInterceptor: vi.fn(),
    currentRoute: routerMocks.currentRoute,
  };
});

// The page reads the workspace list from the stores barrel's workspacesStore
// (a Svelte store exposing .regularWorkspaces) and gates the list on
// workspacePermissions.canCreate. Replace both with controllables so the
// workspace picker renders the options we want.
vi.mock('../stores/workspaces.svelte.js', async () => {
  const { writable } = await import('svelte/store');
  const workspacesStore = writable({
    regularWorkspaces: [
      { id: 1, name: 'Acme', is_personal: false },
      { id: 2, name: 'Other', is_personal: false },
    ],
    // Personal workspace is loaded on-demand; the page checks it in personal
    // mode. Pre-populate it so personal-mode tests don't have to await it.
    personalWorkspace: { id: 3, name: 'Personal', is_personal: true },
  });
  const workspacePermissions = {
    // Tests exercise the unfiltered picker; permission gating is covered by
    // the browser suite (WI-1440).
    canCreate: () => true,
  };
  return {
    workspacesStore,
  };
})

vi.mock('../stores/workspacePermissions.svelte.js', async () => {
  const { writable } = await import('svelte/store');
  const workspacesStore = writable({
    regularWorkspaces: [
      { id: 1, name: 'Acme', is_personal: false },
      { id: 2, name: 'Other', is_personal: false },
    ],
    // Personal workspace is loaded on-demand; the page checks it in personal
    // mode. Pre-populate it so personal-mode tests don't have to await it.
    personalWorkspace: { id: 3, name: 'Personal', is_personal: true },
  });
  const workspacePermissions = {
    // Tests exercise the unfiltered picker; permission gating is covered by
    // the browser suite (WI-1440).
    canCreate: () => true,
  };
  return {
    workspacePermissions,
  };
});

// Sub-item context loads through the same summary endpoint the item detail
// uses (workspace + allowed sub-issue types).
vi.mock('./mobileItemDetailData.js', () => ({
  loadMobileItemDetailSummary: vi.fn(),
}));

// api — spy on items.create so we can assert the parent_id payload.
vi.mock('../api/items.js', () => ({
  items: { create: vi.fn() },
}))

vi.mock('../api/configuration.js', () => ({
  itemTypes: { getAll: vi.fn() },
  itemTemplates: { getAll: vi.fn().mockResolvedValue([]) },
  customFields: { getAll: vi.fn().mockResolvedValue([]) },
  configurationSets: {
        getAll: vi.fn().mockResolvedValue({ configuration_sets: [] }),
        get: vi.fn().mockResolvedValue(null),
      },
  screens: { getFields: vi.fn().mockResolvedValue([]) },
  priorities: { getAll: vi.fn().mockResolvedValue([]) },
}))

vi.mock('../api/milestones.js', () => ({
  milestones: { getAll: vi.fn().mockResolvedValue([]) },
  iterations: { getAll: vi.fn().mockResolvedValue([]) },
  milestoneCategories: { getAll: vi.fn().mockResolvedValue([]) },
}))

vi.mock('../api/time.js', () => ({
  time: { projects: { getByWorkspace: vi.fn().mockResolvedValue([]) } },
}))

vi.mock('../api/workspaces.js', () => ({
  workspaces: {
        get: vi.fn().mockResolvedValue({}),
        // WI-1351: workspace bootstrap and effective-config resolution go
        // through workspaceDataStore, which renders screen fields from here.
        getBootstrap: vi.fn().mockResolvedValue({}),
        getEffectiveConfig: vi.fn().mockResolvedValue({ screens: { create: 55 } }),
      },
}))

vi.mock('../api/users.js', () => ({
  getAssignableUsers: vi.fn().mockResolvedValue([]),
}))

vi.mock('../api/misc.js', () => ({
  labels: {
        getAll: vi.fn().mockResolvedValue([]),
        create: vi.fn(),
      },
}));

import { configurationSets, customFields, itemTemplates, itemTypes, priorities, screens } from '../api/configuration.js';
import { items } from '../api/items.js';
import { workspaces } from '../api/workspaces.js';
import { labels } from '../api/misc.js';
import { milestones } from '../api/milestones.js';
import { i18n } from '../stores/i18n.svelte.js';
import { navigate, setNavigationInterceptor } from '../router.js';
import MobileCreatePage from './MobileCreatePage.svelte';
import { loadMobileItemDetailSummary } from './mobileItemDetailData.js';


import { iterations } from '../api/milestones.js';
import { time } from '../api/time.js';

import { getAssignableUsers } from '../api/users.js';




const PARENT = { id: 777, title: 'Epic: Mobile parity', workspace_id: 1 };
const SUB_TYPES = [
  { id: 10, name: 'Story', hierarchy_level: 1 },
  { id: 11, name: 'Task', hierarchy_level: 1 },
];

function setRoute(query) {
  routerMocks.currentRoute.set({
    path: '/m/new',
    view: 'mobile-create',
    params: {},
    query,
  });
}

afterEach(() => {
  document.body.innerHTML = '';
  sessionStorage.clear();
  vi.clearAllMocks();
  vi.mocked(loadMobileItemDetailSummary).mockReset();
});

beforeEach(async () => {
  // PR #284 migrated the create page onto the i18n runtime; load the real
  // English catalog so t() keys resolve to the copy these tests assert on.
  await i18n.setLocale('en');
  items.create.mockReset();
  itemTypes.getAll.mockReset();
  itemTemplates.getAll.mockReset().mockResolvedValue([]);
  customFields.getAll.mockReset().mockResolvedValue([]);
  configurationSets.getAll.mockReset().mockResolvedValue({ configuration_sets: [] });
  configurationSets.get.mockReset().mockResolvedValue(null);
  screens.getFields.mockReset().mockResolvedValue([]);
  milestones.getAll.mockReset().mockResolvedValue([]);
  iterations.getAll.mockReset().mockResolvedValue([]);
  time.projects.getByWorkspace.mockReset().mockResolvedValue([]);
  priorities.getAll.mockReset().mockResolvedValue([]);
  workspaces.get.mockReset().mockResolvedValue({});
  workspaces.getBootstrap.mockReset().mockResolvedValue({});
  workspaces.getEffectiveConfig.mockReset().mockResolvedValue({ screens: { create: 55 } });
  getAssignableUsers.mockReset().mockResolvedValue([]);
  labels.getAll.mockReset().mockResolvedValue([]);
  labels.create.mockReset();
});

describe('MobileCreatePage — child creation', () => {
  test('renders child context and locks type picker to the allowed sub-issue types', async () => {
    vi.mocked(loadMobileItemDetailSummary).mockResolvedValue({
      item: PARENT,
      available_sub_issue_types: SUB_TYPES,
    });
    setRoute({ parent: '777' });
    render(MobileCreatePage);

    expect(await screen.findByTestId('create-parent')).toHaveTextContent('Epic: Mobile parity');
    expect(screen.getByTestId('editor-title')).toHaveTextContent('New sub-item');

    const typeSelect = await screen.findByTestId('create-type');
    await waitFor(() => {
      const options = [...typeSelect.options].map((o) => o.textContent);
      expect(options).toEqual(['Story', 'Task']);
    });

    // Workspace is locked to the parent's workspace (disabled, Acme selected).
    const wsSelect = screen.getByTestId('create-workspace');
    expect(wsSelect).toBeDisabled();
    expect(wsSelect.value).toBe('1');
    // The parent summary is the only context load — item types are not fetched.
    expect(itemTypes.getAll).not.toHaveBeenCalled();
  });

  test('submit creates the item under the parent with parent_id set and returns to the parent', async () => {
    vi.mocked(loadMobileItemDetailSummary).mockResolvedValue({
      item: PARENT,
      available_sub_issue_types: SUB_TYPES,
    });
    items.create.mockResolvedValue({ id: 999, title: 'New sub' });
    setRoute({ parent: '777' });
    render(MobileCreatePage);

    await fireEvent.input(await screen.findByTestId('create-title'), {
      target: { value: 'New sub' },
    });

    await fireEvent.click(screen.getByTestId('editor-save'));

    await waitFor(() => {
      expect(items.create).toHaveBeenCalledTimes(1);
    });
    expect(items.create).toHaveBeenCalledWith(
      expect.objectContaining({
        title: 'New sub',
        workspace_id: 1,
        item_type_id: 10, // first allowed sub-issue type
        parent_id: 777,
        label_ids: [],
      })
    );
    expect(navigate).toHaveBeenCalledWith('/m/items/777', { replace: true });
  });
});

describe('MobileCreatePage — configured fields (WI-553)', () => {
  test('renders required configured fields and submits their values', async () => {
    items.create.mockResolvedValue({ id: 999, title: 'Screened item' });
    itemTypes.getAll.mockResolvedValue([{ id: 10, name: 'Story' }]);
    configurationSets.getAll.mockResolvedValue({
      configuration_sets: [{ id: 20, is_default: true, workspace_ids: [1] }],
    });
    configurationSets.get.mockResolvedValue({ id: 20, create_screen_id: 55 });
    customFields.getAll.mockResolvedValue([{ id: 7, name: 'Risk', field_type: 'text' }]);
    screens.getFields.mockResolvedValue([
      { id: 1, field_type: 'system', field_identifier: 'story_points', is_required: true },
      { id: 2, field_type: 'custom', field_identifier: '7', is_required: true },
    ]);

    setRoute({});
    render(MobileCreatePage);

    await fireEvent.input(await screen.findByTestId('create-title'), {
      target: { value: 'Screened item' },
    });

    const storyPoints = await screen.findByTestId('configured-system-story_points');
    await fireEvent.input(storyPoints.querySelector('input'), { target: { value: '3' } });

    const risk = await screen.findByTestId('configured-custom-7');
    await fireEvent.input(risk.querySelector('input'), { target: { value: 'High' } });

    await fireEvent.click(screen.getByTestId('editor-save'));

    await waitFor(() => expect(items.create).toHaveBeenCalledTimes(1));
    expect(items.create).toHaveBeenCalledWith(
      expect.objectContaining({
        title: 'Screened item',
        workspace_id: 1,
        item_type_id: 10,
        story_points: 3,
        custom_field_values: { 7: 'High' },
      })
    );
  });

  test('ignores a required linking field that create cannot satisfy', async () => {
    items.create.mockResolvedValue({ id: 999, title: 'No link needed' });
    itemTypes.getAll.mockResolvedValue([{ id: 10, name: 'Story' }]);
    configurationSets.getAll.mockResolvedValue({
      configuration_sets: [{ id: 20, is_default: true, workspace_ids: [1] }],
    });
    configurationSets.get.mockResolvedValue({ id: 20, create_screen_id: 55 });
    customFields.getAll.mockResolvedValue([
      { id: 9, name: 'Blocks', field_type: 'linking' },
    ]);
    screens.getFields.mockResolvedValue([
      { id: 2, field_type: 'custom', field_identifier: '9', is_required: true },
    ]);

    setRoute({});
    render(MobileCreatePage);

    await fireEvent.input(await screen.findByTestId('create-title'), {
      target: { value: 'No link needed' },
    });

    await fireEvent.click(screen.getByTestId('editor-save'));

    await waitFor(() => expect(items.create).toHaveBeenCalledTimes(1));
    expect(items.create).toHaveBeenCalledWith(
      expect.objectContaining({
        title: 'No link needed',
        custom_field_values: {},
      })
    );
    expect(screen.queryByTestId('configured-custom-9')).not.toBeInTheDocument();
  });
});

describe('MobileCreatePage — sheet-based property pickers', () => {
  beforeEach(() => {
    workspaces.get.mockResolvedValue({});
    priorities.getAll.mockResolvedValue([
      { id: 5, name: 'High', color: '#ef4444', sort_order: 1 },
      { id: 6, name: 'Low', color: '#94a3b8', sort_order: 2 },
    ]);
    labels.getAll.mockResolvedValue([
      { id: 31, name: 'bug' },
      { id: 32, name: 'ux' },
    ]);
    milestones.getAll.mockResolvedValue([
      { id: 41, name: 'Sprint 9' },
      { id: 42, name: 'Sprint 10' },
    ]);
  });

  function mockScreenWith(fields) {
    items.create.mockResolvedValue({ id: 999 });
    itemTypes.getAll.mockResolvedValue([{ id: 10, name: 'Story' }]);
    configurationSets.getAll.mockResolvedValue({
      configuration_sets: [{ id: 20, is_default: true, workspace_ids: [1] }],
    });
    configurationSets.get.mockResolvedValue({ id: 20, create_screen_id: 55 });
    customFields.getAll.mockResolvedValue([]);
    screens.getFields.mockResolvedValue(fields);
  }

  test('priority edits through a sheet and lands in the payload', async () => {
    mockScreenWith([
      { id: 1, field_type: 'system', field_identifier: 'priority', is_required: true },
    ]);
    setRoute({});
    render(MobileCreatePage);
    await fireEvent.input(await screen.findByTestId('create-title'), {
      target: { value: 'Prioritized' },
    });
    await fireEvent.click(await screen.findByTestId('create-field-priority'));
    const sheet = await screen.findByTestId('priority-sheet');
    expect(sheet).toBeInTheDocument();
    await fireEvent.click(screen.getByTestId('mobile-sheet-option-5'));
    await waitFor(() => expect(sheet).not.toBeInTheDocument());
    expect(screen.getByTestId('create-field-priority')).toHaveTextContent('High');

    await fireEvent.click(screen.getByTestId('editor-save'));
    await waitFor(() => expect(items.create).toHaveBeenCalledTimes(1));
    expect(items.create).toHaveBeenCalledWith(expect.objectContaining({ priority_id: 5 }));
  });

  test('milestone and labels are multi-select sheets and land in the payload', async () => {
    mockScreenWith([
      { id: 1, field_type: 'system', field_identifier: 'milestone', is_required: true },
      { id: 2, field_type: 'system', field_identifier: 'labels', is_required: true },
    ]);
    setRoute({});
    render(MobileCreatePage);

    await fireEvent.input(await screen.findByTestId('create-title'), {
      target: { value: 'Tagged' },
    });

    // Toggle two milestones; the sheet stays open until Done.
    await fireEvent.click(await screen.findByTestId('create-field-milestone'));
    await screen.findByTestId('milestone-sheet');
    await fireEvent.click(screen.getByTestId('mobile-sheet-option-41'));
    await fireEvent.click(screen.getByTestId('mobile-sheet-option-42'));
    expect(screen.getByTestId('mobile-sheet-option-41')).toHaveAttribute('aria-selected', 'true');
    await fireEvent.click(screen.getByTestId('mobile-sheet-done'));
    await waitFor(() =>
      expect(screen.getByTestId('create-field-milestone')).toHaveTextContent('Sprint 9, Sprint 10')
    );

    // Toggle one label on and back off, then keep the other.
    await fireEvent.click(screen.getByTestId('create-field-labels'));
    await screen.findByTestId('labels-sheet');
    await fireEvent.click(screen.getByTestId('mobile-sheet-option-31'));
    await fireEvent.click(screen.getByTestId('mobile-sheet-option-31'));
    await fireEvent.click(screen.getByTestId('mobile-sheet-option-32'));
    await fireEvent.click(screen.getByTestId('mobile-sheet-done'));

    await fireEvent.click(screen.getByTestId('editor-save'));
    await waitFor(() => expect(items.create).toHaveBeenCalledTimes(1));
    expect(items.create).toHaveBeenCalledWith(
      expect.objectContaining({
        milestone_ids: [41, 42],
        label_ids: [32],
      })
    );
  });
});

describe('MobileCreatePage — draft persistence', () => {
  test('title survives a remount via sessionStorage and clears after create', async () => {
    items.create.mockResolvedValue({ id: 999, title: 'Drafted' });
    itemTypes.getAll.mockResolvedValue([{ id: 10, name: 'Story' }]);

    setRoute({});
    const { unmount } = render(MobileCreatePage);
    await fireEvent.input(await screen.findByTestId('create-title'), {
      target: { value: 'Drafted' },
    });
    expect(sessionStorage.getItem('ws-draft:m-create:work')).toContain('Drafted');
    unmount();

    // A fresh mount (as after a reload) restores the draft.
    setRoute({});
    render(MobileCreatePage);
    expect(await screen.findByTestId('create-title')).toHaveValue('Drafted');

    // Creating clears the draft.

    await fireEvent.click(screen.getByTestId('editor-save'));
    await waitFor(() => expect(items.create).toHaveBeenCalledTimes(1));
    expect(sessionStorage.getItem('ws-draft:m-create:work')).toBeNull();
  });
});

describe('MobileCreatePage — back-gesture guard', () => {
  beforeEach(() => {
    itemTypes.getAll.mockResolvedValue([{ id: 10, name: 'Story' }]);
  });

  test('registers the navigation interceptor while mounted and clears it on unmount', async () => {
    setRoute({});
    const { unmount } = render(MobileCreatePage);
    await screen.findByTestId('create-title');

    expect(setNavigationInterceptor).toHaveBeenCalledTimes(1);
    expect(typeof setNavigationInterceptor.mock.calls[0][0]).toBe('function');

    unmount();
    expect(setNavigationInterceptor).toHaveBeenLastCalledWith(null);
  });

  test('the interceptor lets clean navigation through and vetoes with a confirm sheet when dirty', async () => {
    setRoute({});
    render(MobileCreatePage);
    await screen.findByTestId('create-title');

    const intercept = setNavigationInterceptor.mock.calls[0][0];
    expect(intercept()).toBe(false);
    expect(screen.queryByTestId('create-discard-sheet')).not.toBeInTheDocument();

    await fireEvent.input(screen.getByTestId('create-title'), {
      target: { value: 'Draft' },
    });
    expect(intercept()).toBe(true);
    expect(await screen.findByTestId('create-discard-sheet')).toBeInTheDocument();
  });
});

describe('MobileCreatePage — work item templates (WI-538)', () => {
  test('auto-applies a mandatory template body and locks the description', async () => {
    itemTypes.getAll.mockResolvedValue([{ id: 10, name: 'Bug' }]);
    itemTemplates.getAll.mockResolvedValue([
      { id: 50, name: 'Bug report', mode: 'mandatory', description_body: '## Repro' },
    ]);

    setRoute({});
    render(MobileCreatePage);

    const description = await screen.findByTestId('create-description');
    await waitFor(() => expect(description).toHaveValue('## Repro'));

    // Mandatory lock chip shown; no selectable picker.
    expect(await screen.findByTestId('template-picker-locked')).toHaveTextContent(
      'Bug report (enforced)'
    );
    expect(screen.queryByTestId('template-picker')).not.toBeInTheDocument();
    // Description is locked against editing.
    expect(description).toHaveAttribute('readonly');
  });

  test('offers selectable templates in a picker and applies the chosen body', async () => {
    itemTypes.getAll.mockResolvedValue([{ id: 10, name: 'Task' }]);
    itemTemplates.getAll.mockResolvedValue([
      { id: 60, name: 'Standup', mode: 'selectable', description_body: '- did' },
      { id: 61, name: 'Retro', mode: 'selectable', description_body: '- went' },
    ]);

    setRoute({});
    render(MobileCreatePage);

    const picker = await screen.findByTestId('template-picker');
    await waitFor(() => {
      const options = [...picker.options].map((o) => o.textContent);
      expect(options).toEqual(['No template', 'Standup', 'Retro']);
    });

    await fireEvent.change(picker, { target: { value: '61' } });
    expect(screen.getByTestId('create-description')).toHaveValue('- went');
  });

  test('skips template loading in personal mode and navigates back to Personal on save', async () => {
    items.create.mockResolvedValue({ id: 999, title: 'Chore' });

    setRoute({ mode: 'personal' });
    render(MobileCreatePage);

    // Personal tasks are title-only — no type, no description, no template UI.
    await screen.findByTestId('create-title');
    expect(screen.queryByTestId('template-picker')).not.toBeInTheDocument();
    expect(screen.queryByTestId('template-picker-locked')).not.toBeInTheDocument();
    expect(itemTemplates.getAll).not.toHaveBeenCalled();

    await fireEvent.input(screen.getByTestId('create-title'), {
      target: { value: 'Chore' },
    });

    await fireEvent.click(screen.getByTestId('editor-save'));

    await waitFor(() => expect(items.create).toHaveBeenCalledTimes(1));
    expect(items.create).toHaveBeenCalledWith(
      expect.objectContaining({ title: 'Chore', workspace_id: 3 })
    );
    expect(navigate).toHaveBeenCalledWith('/m/personal', { replace: true });
  });
});
