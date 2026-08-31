<script>
  // Test-only harness for usePullToRefresh. Renders a scroll container bound to
  // the composable and mirrors its reactive state into the DOM (data-*) so the
  // .test.js can assert on it via testing-library.
  import { usePullToRefresh } from './usePullToRefresh.svelte.js';

  let { threshold = 64, maxPull = 96, resistance = 2, onRefresh = null } = $props();

  let host = $state(null);
  // The harness constructs the composable once; individual tests mount it
  // with fixed options instead of changing props after initialization.
  // svelte-ignore state_referenced_locally
  const ptr = usePullToRefresh(() => host, () => onRefresh?.(), {
    threshold,
    maxPull,
    resistance,
  });
</script>

<div
  bind:this={host}
  class="scroll"
  data-testid="ptr-scroll"
  data-pulling={ptr.pulling ? 'true' : 'false'}
  data-pull-distance={String(ptr.pullDistance)}
  data-refreshing={ptr.refreshing ? 'true' : 'false'}
  data-threshold={String(ptr.threshold)}
></div>
