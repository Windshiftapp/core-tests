import { beforeEach, describe, expect, test, vi } from 'vitest';

const extensionState = vi.hoisted(() => ({ data: {} }));

vi.mock('../../stores/extensions.svelte.js', () => ({
  extensions: {
    get value() {
      return extensionState.data;
    },
  },
  getExtensionsForPoint: (data, point) => data[point] || [],
}));

import { BUCKET } from '../buckets.js';
import { adminProvider } from './adminProvider.js';

const t = (key, fallback) => fallback ?? key;

function commandsFor(permissions) {
  return adminProvider({ t, permissions });
}

describe('adminProvider', () => {
  beforeEach(() => {
    extensionState.data = {};
  });

  test('emits nothing without canAccessAdmin', () => {
    expect(commandsFor({ canAccessAdmin: false })).toEqual([]);
  });

  test('emits static registry tabs under /admin', () => {
    const commands = commandsFor({ canAccessAdmin: true });

    expect(commands.length).toBeGreaterThan(0);
    expect(commands.every((command) => command.bucket === BUCKET.ADMIN)).toBe(true);
    expect(commands.every((command) => command.url.startsWith('/admin/'))).toBe(true);
  });

  test('includes plugin-provided admin tabs from the extensions store', () => {
    extensionState.data = {
      'admin.tab': [
        {
          id: 'react-demo-plugin',
          label: 'React Demo App',
          description: 'Demo plugin',
          group: 'Development',
        },
      ],
    };

    const plugin = commandsFor({ canAccessAdmin: true }).find(
      (command) => command.id === 'admin-plugin-react-demo-plugin'
    );

    expect(plugin).toMatchObject({
      label: 'React Demo App',
      description: 'Demo plugin',
      bucket: BUCKET.ADMIN,
      url: '/admin/react-demo-plugin',
    });
  });

  test('skips plugin extensions missing an id or label', () => {
    extensionState.data = {
      'admin.tab': [
        { id: 'no-label', description: 'missing label' },
        { label: 'No id', description: 'missing id' },
      ],
    };

    const commands = commandsFor({ canAccessAdmin: true });

    expect(commands.some((command) => command.id.startsWith('admin-plugin-'))).toBe(false);
  });
});
