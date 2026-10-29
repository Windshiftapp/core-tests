import { expect, type APIRequestContext } from "@playwright/test";

// The `playwright` fixture's type is the playwright-core module namespace.
type PlaywrightModule = typeof import("playwright-core");

/**
 * Read real Stalwart mailboxes over JMAP.
 *
 * The wire-loop fixture provisions genuine accounts (see
 * scripts/provision-stalwart.ts), so tests can inspect what actually landed in
 * a mailbox instead of only asserting on the app's database. Stalwart serves
 * JMAP on the same HTTPS listener as IMAPS with a self-signed certificate, so
 * each call opens a short-lived Playwright request context with TLS
 * verification disabled and HTTP basic credentials for the mailbox.
 */

const JMAP_USING = ["urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"];

export interface StalwartMessage {
	id: string;
	subject: string;
	from: string[];
	to: string[];
	receivedAt: string;
	preview: string;
	text: string;
	html: string;
}

export interface StalwartMailboxOptions {
	username: string;
	password: string;
	/** Base URL of the Stalwart HTTPS listener. Defaults to $STALWART_URL or https://127.0.0.1. */
	baseUrl?: string;
}

interface StalwartWaitOptions extends StalwartMailboxOptions {
	subject?: string | RegExp;
	timeoutMs?: number;
}

function baseUrl(opts: StalwartMailboxOptions): string {
	return (opts.baseUrl ?? process.env.STALWART_URL ?? "https://127.0.0.1").replace(/\/+$/, "");
}

async function openContext(
	playwright: PlaywrightModule,
	opts: StalwartMailboxOptions
): Promise<APIRequestContext> {
	// Stalwart's JMAP endpoint rejects anonymous requests without issuing a
	// WWW-Authenticate challenge, so Playwright's httpCredentials never fires.
	// Send Basic auth preemptively instead.
	const basic = Buffer.from(`${opts.username}:${opts.password}`).toString("base64");
	return playwright.request.newContext({
		baseURL: baseUrl(opts),
		ignoreHTTPSErrors: true,
		extraHTTPHeaders: { Authorization: `Basic ${basic}` },
	});
}

async function jmapCall(
	ctx: APIRequestContext,
	method: string,
	args: Record<string, unknown>
): Promise<Record<string, unknown>> {
	const resp = await ctx.post("/jmap/", {
		data: { using: JMAP_USING, methodCalls: [[method, args, "c1"]] },
	});
	expect(resp.ok(), `${method} HTTP ${resp.status()}`).toBeTruthy();
	const body = (await resp.json()) as {
		methodResponses?: Array<[string, Record<string, unknown>, string]>;
	};
	const [name, payload, tag] = body.methodResponses?.[0] ?? [];
	expect(name, `${method} failed: ${JSON.stringify(payload)}`).not.toBe("error");
	expect(tag, `${method} tag`).toBe("c1");
	if (!payload) throw new Error(`${method} returned no payload`);
	return payload;
}

function joinBody(email: { bodyValues?: Record<string, { value?: string }> }, parts: unknown): string {
	const values = email.bodyValues ?? {};
	if (!Array.isArray(parts)) return "";
	return parts
		.map((part) => values[(part as { partId: string }).partId]?.value ?? "")
		.join("");
}

/** Returns every message in the mailbox, newest first. */
export async function listStalwartMessages(
	playwright: PlaywrightModule,
	opts: StalwartMailboxOptions
): Promise<StalwartMessage[]> {
	const ctx = await openContext(playwright, opts);
	try {
		const session = (await (await ctx.get("/jmap/session")).json()) as {
			accounts?: Record<string, unknown>;
		};
		const accountId = Object.keys(session.accounts ?? {})[0];
		expect(accountId, `JMAP account for ${opts.username}`).toBeTruthy();

		const mailboxes = await jmapCall(ctx, "Mailbox/get", {
			accountId,
			properties: ["name", "role"],
		});
		const inbox = (mailboxes.list as Array<{ id: string; role?: string }> | undefined)?.find(
			(m) => m.role === "inbox"
		);
		expect(inbox, `INBOX for ${opts.username}`).toBeTruthy();

		const query = await jmapCall(ctx, "Email/query", {
			accountId,
			filter: { inMailbox: inbox!.id },
			sort: [{ property: "receivedAt", isAscending: false }],
		});
		const ids = (query.ids as string[] | undefined) ?? [];
		if (ids.length === 0) return [];

		const emails = await jmapCall(ctx, "Email/get", {
			accountId,
			ids,
			properties: [
				"subject",
				"from",
				"to",
				"receivedAt",
				"preview",
				"textBody",
				"htmlBody",
				"bodyValues",
			],
			fetchTextBodyValues: true,
			fetchHTMLBodyValues: true,
		});

		return ((emails.list as Array<Record<string, unknown>> | undefined) ?? []).map((email) => ({
			id: email.id as string,
			subject: (email.subject as string) ?? "",
			from: ((email.from as Array<{ email: string }>) ?? []).map((f) => f.email),
			to: ((email.to as Array<{ email: string }>) ?? []).map((t) => t.email),
			receivedAt: (email.receivedAt as string) ?? "",
			preview: (email.preview as string) ?? "",
			text: joinBody(email, email.textBody),
			html: joinBody(email, email.htmlBody),
		}));
	} finally {
		await ctx.dispose();
	}
}

/** Polls the mailbox until a message matches `subject`, or throws after `timeoutMs`. */
export async function waitForStalwartMessage(
	playwright: PlaywrightModule,
	opts: StalwartWaitOptions
): Promise<StalwartMessage> {
	const timeoutMs = opts.timeoutMs ?? 10_000;
	const deadline = Date.now() + timeoutMs;
	for (;;) {
		const messages = await listStalwartMessages(playwright, opts);
		const match = messages.find((message) => {
			if (!opts.subject) return true;
			return typeof opts.subject === "string"
				? message.subject === opts.subject
				: opts.subject.test(message.subject);
		});
		if (match) return match;
		if (Date.now() >= deadline) {
			throw new Error(
				`No message matching ${String(opts.subject)} in ${opts.username} after ${timeoutMs}ms`
			);
		}
		await new Promise((resolve) => setTimeout(resolve, 250));
	}
}
