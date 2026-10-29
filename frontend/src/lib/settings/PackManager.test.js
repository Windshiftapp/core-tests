import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { writable } from 'svelte/store';
import { afterEach, describe, expect, test, vi } from 'vitest';

const listPacks = vi.hoisted(() => vi.fn());
const applyPack = vi.hoisted(() => vi.fn());
const verifyPack = vi.hoisted(() => vi.fn());

vi.mock('../api/packs.js', () => ({
  packs: { list: listPacks, apply: applyPack, verify: verifyPack },
}));

vi.mock('../stores/workspaces.svelte.js', async () => {
  const { writable: writableStore } = await import('svelte/store');
  return {
    workspacesStore: Object.assign(
          writableStore({ regularWorkspaces: [{ id: 4, name: 'Support', key: 'SUP' }] }),
          { load: vi.fn(), reload: vi.fn() },
        ),
  };
});

vi.mock('../stores/i18n.svelte.js', () => ({
  t: (key) => key,
}));

vi.mock('../stores/toasts.svelte.js', () => ({
  errorToast: vi.fn(),
  successToast: vi.fn(),
}));

import PackManager from './PackManager.svelte';
import { packs } from '../api/packs.js';

afterEach(() => {
  document.body.innerHTML = '';
  vi.clearAllMocks();
});

describe('PackManager', () => {
  test('applies a built-in pack to a new workspace and renders the report', async () => {
    listPacks.mockResolvedValue([
      { name: 'helpdesk', version: '1.0.0', description: 'Support', has_content: true, plugins: [] },
    ]);
    applyPack.mockResolvedValue({
      pack: 'helpdesk',
      pack_version: '1.0.0',
      status: 'applied',
      stages: [
        { name: 'schema', status: 'ok', detail: 'attached' },
        { name: 'content', status: 'ok' },
        { name: 'conformance', status: 'ok' },
      ],
      conformance: { conformant: true, drift_count: 0 },
    });

    render(PackManager);

    await fireEvent.click(await screen.findByTestId('pack-apply-helpdesk'));
    await fireEvent.input(screen.getByTestId('pack-new-name'), {
      target: { value: 'Helpdesk' },
    });
    await fireEvent.click(screen.getByTestId('pack-run-apply'));

    await waitFor(() => {
      expect(applyPack).toHaveBeenCalledWith('helpdesk', { workspace_name: 'Helpdesk' });
    });

    const report = await screen.findByTestId('pack-report');
    expect(report).toBeTruthy();
    expect(await screen.findByTestId('pack-stage-schema')).toBeTruthy();
    expect(screen.getByTestId('pack-conformance')).toBeTruthy();
  });
});
