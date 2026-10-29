<script>
  // Test stub for FilterBuilderModal: exercises FilterMenu's apply/save
  // plumbing without mounting the dynamic field builder.
  let {
    isOpen = $bindable(false),
    initialRows = [],
    startInSaveMode = false,
    canShare = false,
    onApply = () => {},
    onSave = async () => ({ ok: true }),
  } = $props();

  const sampleRows = [
    { field: { id: 'priority', type: 'text' }, operator: '=', value: 'High' },
  ];
</script>

{#if isOpen}
  <div
    data-testid="filter-builder-stub"
    data-can-share={String(canShare)}
    data-save-mode={String(startInSaveMode)}
  >
    <button type="button" data-testid="stub-apply" onclick={() => onApply(initialRows)}>apply</button>
    <button
      type="button"
      data-testid="stub-save-personal"
      onclick={() => onSave({ name: 'Stub', shared: false, rows: sampleRows })}
    >
      save personal
    </button>
    <button
      type="button"
      data-testid="stub-save-shared"
      onclick={() => onSave({ name: 'Stub', shared: true, rows: sampleRows })}
    >
      save shared
    </button>
  </div>
{/if}
