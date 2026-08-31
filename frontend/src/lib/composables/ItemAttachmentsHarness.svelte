<script>
  import { useItemAttachments } from './useItemAttachments.svelte.js';

  let { onready } = $props();
  const manager = useItemAttachments(() => 42);

  $effect(() => {
    onready(manager);
  });
</script>
