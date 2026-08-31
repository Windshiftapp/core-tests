import { render } from '@testing-library/svelte';
import { afterEach, beforeAll, describe, expect, test, vi } from 'vitest';

// jsdom does not implement the Web Animations API. Svelte 5 transitions
// call element.animate during outro. Stub it so transitions resolve.
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
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {};
  }
});

// MentionPicker fetches users when opened. Provide a fixed roster.
vi.mock('../api.js', () => ({
  api: {
    getUsers: vi.fn(async () => [
      { username: 'alice', first_name: 'Alice', last_name: 'Johnson' },
      { username: 'bob', first_name: 'Bob', last_name: 'Smith' },
    ]),
    getAssignableUsers: vi.fn(async () => [
      { username: 'alice', first_name: 'Alice', last_name: 'Johnson' },
      { username: 'bob', first_name: 'Bob', last_name: 'Smith' },
    ]),
  },
}));

// i18n.t — return the key verbatim so assertions don't depend on locale data.
vi.mock('../stores/i18n.svelte.js', () => ({
  t: vi.fn((key) => key),
}));

// Stub the child presentational components (Svelte 5 component-mock style).
vi.mock('../components/Avatar.svelte', () => ({
  default: function MockAvatar() {},
}));
vi.mock('../components/Text.svelte', () => ({
  default: function MockText() {},
}));

// Capture the document-level keydown handler that runed's useEventListener
// registers, so the test can dispatch real KeyboardEvents at it. The picker
// listens in the capture phase — mirror that so the test path matches prod.
let keydownHandler = null;
let registeredCapture = null;
vi.mock('runed', () => ({
  useEventListener: (_target, _event, handler, options) => {
    // target is a thunk returning document; we only care about keydown.
    keydownHandler = handler;
    registeredCapture = options?.capture ?? false;
    document.addEventListener('keydown', handler, options);
  },
}));

import { api } from '../api.js';
import MentionPicker from './MentionPicker.svelte';

afterEach(() => {
  if (keydownHandler) {
    document.removeEventListener('keydown', keydownHandler, {
      capture: registeredCapture,
    });
  }
  keydownHandler = null;
  registeredCapture = null;
  document.body.innerHTML = '';
  vi.clearAllMocks();
});

function pressKey(key) {
  const event = new KeyboardEvent('keydown', {
    key,
    bubbles: true,
    cancelable: true,
  });
  // Spy on the instance methods rather than the listener wrapper so we can
  // assert the picker consumed the key.
  const preventDefault = vi.spyOn(event, 'preventDefault');
  const stopPropagation = vi.spyOn(event, 'stopPropagation');
  document.dispatchEvent(event);
  return { event, preventDefault, stopPropagation };
}

describe('MentionPicker — shared workspace roster', () => {
  test('loads the same actionable roster as the assignee picker', async () => {
    api.getAssignableUsers.mockResolvedValueOnce([
      {
        username: 'human',
        first_name: 'Human',
        last_name: 'Member',
        is_agent: false,
      },
      {
        username: 'local-agent',
        first_name: 'Local',
        last_name: 'Agent',
        is_agent: true,
        agent_presence: 'local',
      },
    ]);

    render(MentionPicker, { props: { open: true, workspaceId: 42 } });

    await vi.waitFor(() => {
      expect(document.querySelectorAll('[role="option"]')).toHaveLength(2);
    });
    expect(api.getAssignableUsers).toHaveBeenCalledWith(42);
    expect(api.getUsers).not.toHaveBeenCalled();
  });
});

describe('MentionPicker — Enter while open must not break the mention (WI-200)', () => {
  test('Enter with results selects the highlighted user and consumes the key', async () => {
    const onSelect = vi.fn();
    render(MentionPicker, { props: { open: true, onSelect } });

    // Opening the picker kicks off getUsers(); wait for the list to populate.
    await vi.waitFor(() => {
      expect(document.querySelector('[role="option"]')).toBeTruthy();
    });

    const { preventDefault, stopPropagation } = pressKey('Enter');

    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ username: 'alice' }));
    expect(preventDefault).toHaveBeenCalled();
    expect(stopPropagation).toHaveBeenCalled();
  });

  test('Enter with zero matching results consumes the key but selects nothing', async () => {
    // This is the WI-200 regression: previously the handler bailed early on
    // an empty filtered list, leaving Enter to fall through to ProseMirror,
    // which inserted a newline at the cursor and split the in-progress
    // @mention into a broken chip.
    const onSelect = vi.fn();
    render(MentionPicker, {
      props: { open: true, query: 'zzznomatch', onSelect },
    });

    await vi.waitFor(() => {
      expect(document.querySelector('[role="option"]')).toBeNull();
    });

    const { preventDefault, stopPropagation } = pressKey('Enter');

    expect(onSelect).not.toHaveBeenCalled();
    // The key must still be consumed so the editor can't insert a newline.
    expect(preventDefault).toHaveBeenCalled();
    expect(stopPropagation).toHaveBeenCalled();
  });

  test('Enter is a no-op (not consumed) when the picker is closed', async () => {
    const onSelect = vi.fn();
    render(MentionPicker, { props: { open: false, onSelect } });

    const { preventDefault } = pressKey('Enter');

    expect(onSelect).not.toHaveBeenCalled();
    expect(preventDefault).not.toHaveBeenCalled();
    expect(api.getUsers).not.toHaveBeenCalled();
  });

  test('Escape cancels and consumes the key even with zero results', async () => {
    const onCancel = vi.fn();
    render(MentionPicker, {
      props: { open: true, query: 'zzznomatch', onCancel },
    });

    await vi.waitFor(() => {
      expect(document.querySelector('[role="option"]')).toBeNull();
    });

    const { preventDefault, stopPropagation } = pressKey('Escape');

    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(preventDefault).toHaveBeenCalled();
    expect(stopPropagation).toHaveBeenCalled();
  });

  test('ArrowDown navigates the list and consumes the key', async () => {
    const onSelect = vi.fn();
    render(MentionPicker, { props: { open: true, onSelect } });

    await vi.waitFor(() => {
      expect(document.querySelectorAll('[role="option"]').length).toBe(2);
    });

    const { preventDefault } = pressKey('ArrowDown');
    pressKey('Enter');

    expect(preventDefault).toHaveBeenCalled();
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ username: 'bob' }));
  });
});

describe('MentionPicker — stays on the visible viewport on mobile (WI-431)', () => {
  // jsdom reports offsetWidth/offsetHeight as 0 (no layout), so the picker
  // falls back to its CSS max bounds (320x300) for the clamp. That's the same
  // worst case the component guards against, so the assertions below are exact.
  const EDGE_MARGIN = 8;

  afterEach(() => {
    delete window.visualViewport;
  });

  function styleOf(el) {
    return { top: el.style.top, left: el.style.left };
  }

  test('clamps a bottom/right cursor into the window when no keyboard is open', async () => {
    window.innerWidth = 320;
    window.innerHeight = 568;
    // Cursor near the bottom-right edge — raw coords would push the menu
    // off-screen on both axes.
    render(MentionPicker, {
      props: { open: true, position: { x: 300, y: 540 } },
    });

    await vi.waitFor(() => {
      expect(document.querySelector('.mention-picker')).toBeTruthy();
    });
    const el = document.querySelector('.mention-picker');

    // maxX = max(8, 320 - 320 - 8) = 8 ; maxY = max(8, 568 - 300 - 8) = 260
    expect(styleOf(el)).toEqual({ left: '8px', top: '260px' });
  });

  test('clamps against visualViewport so the menu clears the on-screen keyboard', async () => {
    // Full window is tall, but the keyboard leaves only 300px visible.
    window.innerWidth = 390;
    window.innerHeight = 844;
    window.visualViewport = {
      width: 390,
      height: 300,
      offsetLeft: 0,
      offsetTop: 0,
    };

    // Composer pinned to the bottom of the *visible* area: cursor at y ~ 290.
    render(MentionPicker, {
      props: { open: true, position: { x: 20, y: 290 } },
    });

    await vi.waitFor(() => {
      expect(document.querySelector('.mention-picker')).toBeTruthy();
    });
    const el = document.querySelector('.mention-picker');

    // Without visualViewport this would clamp to innerHeight (844-300-8=536),
    // leaving top:290 -> menu renders into the keyboard. With it:
    // maxY = max(8, 300 - 300 - 8) = 8, so the menu is pulled up on-screen.
    expect(el.style.top).toBe(`${EDGE_MARGIN}px`);
  });
});
