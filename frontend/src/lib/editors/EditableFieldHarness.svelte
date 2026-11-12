<script>
  import EditableField from './EditableField.svelte';

  let { value = '', oncommit = null, editable = true, multiline = false } = $props();
</script>

<EditableField {value} {editable} {multiline} {oncommit}>
  {#snippet display()}
    <span data-testid="display">{value}</span>
  {/snippet}
  {#snippet editor({ draft, setDraft, error })}
    <input
      data-testid="editor"
      value={draft ?? ''}
      oninput={(e) => setDraft(e.currentTarget.value)}
    />
    {#if error}<span data-testid="error">{error}</span>{/if}
  {/snippet}
</EditableField>
