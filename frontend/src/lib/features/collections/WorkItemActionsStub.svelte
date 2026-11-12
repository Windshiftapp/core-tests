<script>
  // Test stub for the row-action DropdownMenu: exposes the action count only.
  let { items = [] } = $props();
</script>

<button type="button" data-testid="stub-actions">{items.length}</button>
