import { beforeEach, describe, expect, it } from 'vitest';

const { formBuilderStore } = await import('./formBuilderStore.svelte.js');
const { screenEditorStore } = await import('./screenEditorStore.svelte.js');
const { DragStateStore } = await import('./DragStateStore.svelte.js');

describe('shared drag-state composition', () => {
  beforeEach(() => {
    formBuilderStore.reset();
    screenEditorStore.reset();
  });

  it.each([
    ['formBuilderStore', formBuilderStore],
    ['screenEditorStore', screenEditorStore],
  ])('%s exposes drag state via the shared DragStateStore base', (_name, store) => {
    expect(store).toBeInstanceOf(DragStateStore);
  });

  it('sets and clears per-field drag state through the inherited methods', () => {
    formBuilderStore.setDragState(7, { closestEdge: 'bottom' });
    expect(formBuilderStore.fieldDragState.get(7)).toEqual({ closestEdge: 'bottom' });

    formBuilderStore.clearDragState();
    expect(formBuilderStore.fieldDragState.get(7)).toEqual({ closestEdge: null });

    screenEditorStore.setDragState(9, { closestEdge: 'top' });
    expect(screenEditorStore.fieldDragState.get(9)).toEqual({ closestEdge: 'top' });
    screenEditorStore.clearDragState();
    expect(screenEditorStore.fieldDragState.get(9)).toEqual({ closestEdge: null });
  });

  it('tracks the dragged field and resets it via resetDragState', () => {
    formBuilderStore.setDraggedField({ id: 1 });
    expect(formBuilderStore.draggedField).toEqual({ id: 1 });

    screenEditorStore.setDraggedField(true);
    expect(screenEditorStore.draggedField).toBe(true);

    screenEditorStore.clearDraggedField();
    expect(screenEditorStore.draggedField).toBeNull();
  });

  it('resetDragState clears both stores after a full reset', () => {
    formBuilderStore.setDragState(1, { closestEdge: 'bottom' });
    formBuilderStore.setDraggedField({ id: 1 });

    // Full reset() must clear drag state without leaving stale entries.
    formBuilderStore.reset();
    expect(formBuilderStore.fieldDragState.size).toBe(0);
    expect(formBuilderStore.draggedField).toBeNull();
  });

  it('keeps form-builder and screen-editor drag state independent', () => {
    formBuilderStore.setDragState(1, { closestEdge: 'bottom' });
    screenEditorStore.setDragState(2, { closestEdge: 'top' });

    expect(formBuilderStore.fieldDragState.has(2)).toBe(false);
    expect(screenEditorStore.fieldDragState.has(1)).toBe(false);
  });
});
