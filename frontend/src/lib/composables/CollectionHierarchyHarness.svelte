<script>
  import { useCollectionHierarchy } from './useCollectionHierarchy.svelte.js';

  let {
    onready,
    maxDepth = 0,
    view = null,
    workspaceId = null,
    collectionId = null,
  } = $props();
  const hierarchy = useCollectionHierarchy({
    getMaxDepth: () => maxDepth,
    getView: () => view,
    getWorkspaceId: () => workspaceId,
    getCollectionId: () => collectionId,
  });

  $effect(() => {
    onready(hierarchy);
  });
</script>
