<script>
  // Test stub for IterationBurndown: records that the shared layout rendered it.
  let { data = null } = $props();
</script>

<div data-testid="iteration-burndown" data-has-data={data != null}></div>
