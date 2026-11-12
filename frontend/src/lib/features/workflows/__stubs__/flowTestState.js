// Shared state between the SvelteFlow stub and the spec. The stub stands in for
// @xyflow/svelte, which cannot render edges in jsdom; the spec drives the same
// callbacks the library would.
export const flowTestState = {
  connectPayload: null,
};
