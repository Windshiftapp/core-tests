<script>
  // Test harness for the EntityManagerPage scaffold: supplies the smallest
  // possible page so the test can drive the shared list/form/delete shape.
  import EntityManagerPage from './EntityManagerPage.svelte';

  let { load, create, update, remove } = $props();

  let form = $state({ name: '' });

  const columns = [
    { key: 'name', label: 'Name' },
    { key: 'actions', label: 'Actions', slot: 'actions' },
  ];
</script>

<EntityManagerPage
  title="Widgets"
  addLabel="Add widget"
  addTestid="widget-add"
  emptyMessage="No widgets"
  {columns}
  {load}
  {create}
  {update}
  {remove}
  onOpenCreate={() => {
    form = { name: '' };
  }}
  onOpenEdit={(item) => {
    form = { name: item.name };
  }}
  confirmDeleteMessage={(item) => `Delete ${item.name}?`}
  rowTestid={(item) => `widget-row-${item.id}`}
  actionTestidPrefix="widget"
  submitDisabled={() => !form.name.trim()}
  formFields={fields}
>
</EntityManagerPage>

{#snippet fields()}
  <input data-testid="widget-name" bind:value={form.name} />
{/snippet}
