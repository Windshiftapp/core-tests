<script>
  // Test stub: exposes the props SubFilterBar passes so the bar's store sync can
  // be asserted without mounting the real menu.
  let { activeRows = [], applied = false } = $props();
</script>

<div
  data-testid="filter-menu-stub"
  data-active-count={activeRows.length}
  data-applied={String(applied)}
></div>
