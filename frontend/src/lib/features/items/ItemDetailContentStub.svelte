<script>
  // Minimal ItemDetailContent stand-in that exposes the item identity the
  // component is currently displaying.
  let { item } = $props();
</script>

<div data-testid="item-detail-ready">{item?.title ?? ''}</div>
