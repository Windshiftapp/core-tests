import { fireEvent, render, screen } from '@testing-library/svelte';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('../api/users.js', () => ({
  getAssignableUsers: vi.fn(),
  getUsers: vi.fn(),
}));

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

import { getAssignableUsers, getUsers } from '../api/users.js';
import UserPicker from './UserPicker.svelte';

beforeAll(() => {
  if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {};
  if (!globalThis.ResizeObserver) {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  }
});

beforeEach(() => {
  vi.clearAllMocks();
  getAssignableUsers.mockResolvedValue([]);
  getUsers.mockResolvedValue([]);
});

describe('UserPicker provided options', () => {
  it('does not fetch assignable users on mount when users are provided', async () => {
    render(UserPicker, { props: { users: [], value: null, workspaceId: 5 } });

    await Promise.resolve();
    expect(getAssignableUsers).not.toHaveBeenCalled();
  });

  it('calls onOpen when the picker opens instead of loading on mount', async () => {
    const onOpen = vi.fn();
    render(UserPicker, { props: { users: [], value: null, workspaceId: 5, onOpen } });

    await fireEvent.click(screen.getByTestId('user-picker-trigger'));

    expect(onOpen).toHaveBeenCalledTimes(1);
    expect(getAssignableUsers).not.toHaveBeenCalled();
  });
});

// Two people can share a display name. The username is the only stable handle,
// so every option row must show it and the trigger must add it whenever the
// selected name is duplicated in the roster.
describe('UserPicker disambiguates duplicate display names', () => {
  const roster = [
    { id: 1, username: 'jdoe', first_name: 'John', last_name: 'Doe' },
    { id: 2, username: 'john.doe', first_name: 'John', last_name: 'Doe' },
    { id: 3, username: 'asmith', first_name: 'Alice', last_name: 'Smith' },
  ];

  it('shows the username on every option row', async () => {
    render(UserPicker, { props: { users: roster, value: null, workspaceId: 5 } });

    await fireEvent.click(screen.getByTestId('user-picker-trigger'));

    expect(await screen.findByTestId('user-picker-option-1')).toHaveTextContent('@jdoe');
    expect(screen.getByTestId('user-picker-option-2')).toHaveTextContent('@john.doe');
    expect(screen.getByTestId('user-picker-option-3')).toHaveTextContent('@asmith');
  });

  it('shows the username in the trigger when the selected name is duplicated', () => {
    render(UserPicker, { props: { users: roster, value: 1, workspaceId: 5 } });

    expect(screen.getByTestId('user-picker-trigger')).toHaveTextContent('@jdoe');
  });

  it('omits the username in the trigger when the selected name is unique', () => {
    render(UserPicker, { props: { users: roster, value: 3, workspaceId: 5 } });

    const trigger = screen.getByTestId('user-picker-trigger');
    expect(trigger).toHaveTextContent('Alice Smith');
    expect(trigger).not.toHaveTextContent('@asmith');
  });
});
