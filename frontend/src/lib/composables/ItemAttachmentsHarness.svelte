<script>
  import { useItemAttachments } from './useItemAttachments.svelte.js';

  let { onready, showError } = $props();
  const manager = useItemAttachments(() => 42, (title, message) =>
    (showError ?? console.error)(title, message)
  );

  $effect(() => {
    onready(manager);
  });
</script>
