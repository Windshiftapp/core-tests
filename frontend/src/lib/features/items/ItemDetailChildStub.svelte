<script>
  // Generic no-op stand-in for ItemDetail's dialogs and panels. Renders its
  // children (if any) so a wrapping stub can still observe content.
  let { children } = $props();
</script>

{@render children?.()}
