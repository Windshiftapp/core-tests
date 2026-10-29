<script>
  // Minimal stand-in for the read-only Milkdown surface: one external link and
  // one plain text span, both inside the description display wrapper.
  let { content = '' } = $props();
</script>

<div data-testid="description-markdown-stub" data-content={content}>
  <a href="https://example.com/link" data-testid="description-link">external link</a>
  <span data-testid="description-plain-text">plain text</span>
</div>
