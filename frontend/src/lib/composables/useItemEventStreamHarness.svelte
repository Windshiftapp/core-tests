<script>
  import { useItemEventStream } from './useItemEventStream.svelte.js';

  let { itemId = 1, onReconcile, onDeleted } = $props();

  const stream = useItemEventStream(() => itemId, {
    onReconcile: () => onReconcile?.(),
    onDeleted: () => onDeleted?.(),
  });

  export function isConnected() {
    return stream.connected;
  }
</script>
