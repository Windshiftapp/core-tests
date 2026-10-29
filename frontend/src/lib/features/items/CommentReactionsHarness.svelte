<script>
	// Test harness: keeps reaction state like Comments.svelte does so the
	// optimistic toggle and the server response both re-render the chips.
	import CommentReactions from './CommentReactions.svelte';

	let { initialReactions = [], canReact = true, onError } = $props();
	// svelte-ignore state_referenced_locally
	let reactions = $state(initialReactions);
</script>

<CommentReactions
	comment={{ id: 11, reactions }}
	{canReact}
	onChange={(next) => (reactions = next)}
	{onError}
/>
