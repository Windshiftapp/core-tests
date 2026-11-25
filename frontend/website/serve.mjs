import { readFileSync } from "node:fs";
import { readFile, stat } from "node:fs/promises";
import { createServer } from "node:http";
import { extname, join, resolve, sep } from "node:path";

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
const mimeTypes = {
	".html": "text/html; charset=utf-8",
	".js": "text/javascript; charset=utf-8",
	".mjs": "text/javascript; charset=utf-8",
	".css": "text/css; charset=utf-8",
	".json": "application/json",
	".svg": "image/svg+xml",
	".png": "image/png",
	".jpg": "image/jpeg",
	".jpeg": "image/jpeg",
	".webp": "image/webp",
	".gif": "image/gif",
	".ico": "image/x-icon",
	".woff": "font/woff",
	".woff2": "font/woff2",
	".txt": "text/plain; charset=utf-8",
	".pdf": "application/pdf",
};

async function isFile(path) {
	try {
		return (await stat(path)).isFile();
	} catch (error) {
		if (error.code === "ENOENT" || error.code === "ENOTDIR") return false;
		throw error;
	}
}

const server = createServer(async (request, response) => {
	try {
		const pathname = decodeURIComponent(
			new URL(request.url, "http://localhost").pathname,
		);
		let path = resolve(root, `.${pathname}`);
		if (path !== root && !path.startsWith(`${root}${sep}`)) {
			response.writeHead(404).end("Not found");
			return;
		}
		if (!(await isFile(path)) || !pathname.split("/").at(-1).includes(".")) {
			path = join(path, "index.html");
		}
		if (!(await isFile(path))) {
			response.writeHead(404).end("Not found");
			return;
		}
		const body = await readFile(path);
		response
			.writeHead(200, {
				"Content-Type": mimeTypes[extname(path)] ?? "application/octet-stream",
				"Content-Security-Policy": csp,
			})
			.end(body);
	} catch (error) {
		response
			.writeHead(error instanceof URIError ? 400 : 500)
			.end("Invalid request");
	}
});
server.listen(Number(process.env.PORT ?? 4199), "127.0.0.1", () => {
	process.stdout.write(
		`WEBSITE_PREVIEW_URL=http://127.0.0.1:${server.address().port}\n`,
	);
});
