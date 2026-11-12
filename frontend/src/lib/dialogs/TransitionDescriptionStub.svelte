<script>
  // Minimal stand-in for the lazy Milkdown surface used by the transition
  // screen form. It exposes the bound markdown so the seeding contract can be
  // asserted without booting ProseMirror in jsdom.
  let { content = $bindable(''), testId = null } = $props();
</script>

<div data-testid={testId} data-content={content}></div>
