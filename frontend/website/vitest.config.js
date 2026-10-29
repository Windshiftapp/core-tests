import { fileURLToPath } from "node:url";

export default {
	test: {
		include: [fileURLToPath(new URL("./*.test.js", import.meta.url))],
		environment: "node",
	},
};
