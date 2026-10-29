<script>
  import { useCollectionEventStream } from './useCollectionEventStream.svelte.js';

  let { scope, onInvalidate } = $props();

  const stream = useCollectionEventStream(() => scope, {
    onInvalidate: () => onInvalidate?.(),
  });

  export function isConnected() {
    return stream.connected;
  }
</script>
