<script>
  let { user = null } = $props();
</script>

<div data-testid="regional-settings-stub">
  <span data-testid="regional-settings-language">{user?.language}</span>
  <span data-testid="regional-settings-timezone">{user?.timezone}</span>
</div>
