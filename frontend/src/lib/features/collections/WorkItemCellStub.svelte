<script>
  // Test stub for ListCellRenderer: echoes the props the shared row forwards so
  // tests can assert the cell wiring without the real editor dependencies.
  let { item, column, canEdit = false } = $props();
</script>

<span data-testid="stub-cell" data-column={column.field_identifier} data-can-edit={canEdit}>
  {item.title}:{column.field_identifier}
</span>
