import { beforeEach, describe, expect, it, vi } from 'vitest';

const { update } = vi.hoisted(() => ({ update: vi.fn() }));

vi.mock('../api.js', () => ({
  api: {
    requestTypes: {
      update,
    },
  },
}));

const { formBuilderStore } = await import('./formBuilderStore.svelte.js');

describe('formBuilderStore save state', () => {
  beforeEach(() => {
    update.mockReset();
    update.mockResolvedValue({});
    formBuilderStore.reset();
    formBuilderStore.channelId = 3;
    formBuilderStore.showFieldEditor = true;
    formBuilderStore.editingForm = { id: 7, is_active: true };
    formBuilderStore.routingMeta = {
      name: 'Support',
      description: '',
      icon: 'FileText',
      color: '#14b8a6',
      workspace_id: 11,
      item_type_id: 4,
    };
    formBuilderStore.markBuilderSaved();
  });

  it('tracks routing edits and marks them saved after persistence', async () => {
    expect(formBuilderStore.hasUnsavedChanges).toBe(false);

    formBuilderStore.routingMeta.name = 'Customer support';
    expect(formBuilderStore.hasUnsavedChanges).toBe(true);

    await formBuilderStore.saveRoutingMetadata();

    expect(update).toHaveBeenCalledWith(3, 7, {
      name: 'Customer support',
      description: '',
      icon: 'FileText',
      color: '#14b8a6',
      item_type_id: 4,
      workspace_id: 11,
      is_active: true,
    });
    expect(formBuilderStore.hasUnsavedChanges).toBe(false);
  });
});
