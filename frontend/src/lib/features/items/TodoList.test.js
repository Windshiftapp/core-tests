import { render, screen } from '@testing-library/svelte';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../api.js', () => ({
  api: {
    items: {
      create: vi.fn(),
      getAll: vi.fn(),
      transition: vi.fn(),
    },
    statusCategories: { getAll: vi.fn() },
    workspaces: { getStatuses: vi.fn() },
  },
}));

vi.mock('../../stores', () => ({
  authStore: { currentUser: { id: 7 } },
}));

vi.mock('../../stores/toasts.svelte.js', () => ({ errorToast: vi.fn() }));

vi.mock('@lucide/svelte', () => ({
  Check: vi.fn(() => null),
  ChevronDown: vi.fn(() => null),
  ChevronRight: vi.fn(() => null),
  Plus: vi.fn(() => null),
  Trash2: vi.fn(() => null),
  X: vi.fn(() => null),
}));

vi.mock('./WorkItemRow.svelte', () => ({ default: function MockComponent() {} }));
vi.mock('../../dialogs/DeleteItemDialog.svelte', () => ({ default: function MockComponent() {} }));
vi.mock('./ItemDetail.svelte', () => ({ default: function MockComponent() {} }));
vi.mock('../personal/PersonalTaskDetail.svelte', () => ({ default: function MockComponent() {} }));
vi.mock('../../components/Checkbox.svelte', () => ({ default: function MockComponent() {} }));
vi.mock('../../components/EmptyState.svelte', () => ({ default: function MockComponent() {} }));

const { api } = await import('../../api.js');
const { i18n } = await import('../../stores/i18n.svelte.js');
const { default: TodoList } = await import('./TodoList.svelte');

describe('TodoList completed-task history filter', () => {
  beforeAll(async () => {
    await i18n.setLocale('en');
  });

  beforeEach(() => {
    localStorage.clear();
    api.items.getAll.mockResolvedValue({ items: [] });
    api.statusCategories.getAll.mockResolvedValue([]);
    api.workspaces.getStatuses.mockResolvedValue([]);
  });

  it('explains that the range only limits completed task history', async () => {
    render(TodoList, { props: { workspaceId: 42 } });

    expect(await screen.findByText('Completed task history')).toBeInTheDocument();
    expect(screen.getByText('Open tasks are always shown.')).toBeInTheDocument();
    expect(screen.getByText('Last 7 days')).toBeInTheDocument();
    expect(screen.getByText('Last 30 days')).toBeInTheDocument();
    expect(screen.getByText('Last 90 days')).toBeInTheDocument();
    expect(screen.getByLabelText('Show tasks completed since date')).toBeInTheDocument();
  });
});
