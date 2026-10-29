<script>
  // Distinct stand-in for TimeLogModal so ItemDetail tests can assert whether
  // the dialog is mounted without pulling in the real form.
  let { editingWorklog = null } = $props();
</script>

<div data-testid="time-log-modal-stub">{editingWorklog ? 'edit' : 'create'}</div>
