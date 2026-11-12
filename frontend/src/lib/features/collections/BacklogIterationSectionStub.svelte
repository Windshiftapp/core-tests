<script>
  // Renders the section identity so tests can assert which iteration sections
  // the backlog chose to display without pulling in the full row tree.
  let { iteration = null, items = [] } = $props();
</script>

<div
  data-testid={`backlog-iteration-section-${iteration ? iteration.id : 'unassigned'}`}
  data-iteration-status={iteration?.status ?? 'unassigned'}
>
  {iteration?.name ?? 'Backlog'} ({items.length})
</div>
