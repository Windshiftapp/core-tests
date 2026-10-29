import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";

const root = resolve(process.env.WINDSHIFT_WEBSITE_DIST);
const security = readFileSync(
	new URL(
		"../../../../websites/windshift-website/backend-go/middleware/security.go",
		import.meta.url,
	),
	"utf8",
);
const analytics = security.match(/const analyticsOrigin = "([^"]+)"/)[1];
const csp = security
	.match(/const productionCSP = (.+)/)[1]
	.split(" + ")
	.map((part) => (part === "analyticsOrigin" ? analytics : JSON.parse(part)))
	.join("");
Bun.serve({
	hostname: "127.0.0.1",
	port: 4199,
	async fetch(request) {
		const pathname = new URL(request.url).pathname;
		const path = resolve(root, `.${pathname}`);
		if (path !== root && !path.startsWith(`${root}/`))
			return new Response("Not found", { status: 404 });
		let file = Bun.file(path);
		if (!(await file.exists()) || !pathname.split("/").at(-1).includes("."))
			file = Bun.file(join(path, "index.html"));
		if (!(await file.exists()))
			return new Response("Not found", { status: 404 });
		return new Response(file, { headers: { "Content-Security-Policy": csp } });
	},
});
