<script>
  // Test double for @xyflow/svelte's SvelteFlow. It renders a clickable button
  // per edge and invokes the callbacks with the payload shapes the real
  // library uses: onedgeclick gets `{ event, edge }`, onconnect gets the
  // connection params.
  import { flowTestState } from './flowTestState.js';

  let {
    nodes = $bindable([]),
    edges = $bindable([]),
    onedgeclick = null,
    onconnect = null,
    children
  } = $props();
</script>

<div data-testid="flow-stub">
  {#each edges as edge (edge.id)}
    <button
      type="button"
      data-testid={`flow-edge-${edge.id}`}
      onclick={(event) => onedgeclick?.({ edge, event })}
    >
      {edge.id}
    </button>
  {/each}
  {#if onconnect}
    <button
      type="button"
      data-testid="flow-connect"
      onclick={() => onconnect(flowTestState.connectPayload)}
    >
      connect
    </button>
  {/if}
  {@render children?.()}
</div>
