import { expect, test } from "../fixtures/mail";
import { createWorkspaceViaAPI } from "../fixtures/api-helpers";
import { generateWorkspace } from "../fixtures/test-data";
import {
	createPortalChannel,
	signInViaMagicLink,
	type PortalChannelHandle,
} from "../helpers/portal-setup";
import { waitForStalwartMessage } from "../helpers/stalwart";
import tls from "node:tls";

const SEC_FETCH = { "Sec-Fetch-Site": "same-origin" };
const STALWART_IMAPS = Number(process.env.STALWART_IMAPS_PORT ?? 993);
const STALWART_SMTPS = Number(process.env.STALWART_SMTPS_PORT ?? 465);
// Provisioned by run-e2e.sh over Stalwart's JMAP management API. The recovery
// admin cannot run UID SEARCH, so the intake channel needs a real mailbox.
// The fallbacks keep ad-hoc runs working against an already-provisioned server.
const STALWART_IMAP_USERNAME =
	process.env.STALWART_IMAP_USERNAME ?? "wire-intake@windshift-wire.test";
const STALWART_IMAP_PASSWORD =
	process.env.STALWART_IMAP_PASSWORD ?? "WireLoop-Intake-7Qz";
const STALWART_CUSTOMER_USERNAME =
	process.env.STALWART_CUSTOMER_USERNAME ?? "wire-customer@windshift-wire.test";
const STALWART_CUSTOMER_PASSWORD =
	process.env.STALWART_CUSTOMER_PASSWORD ?? "WireLoop-Customer-42x";
const CUSTOMER_ADDRESS = "wire-customer@windshift-wire.test";

/**
 * Minimal SMTPS submission client: EHLO, AUTH PLAIN, MAIL FROM, RCPT TO, DATA,
 * QUIT — exactly enough to send the customer's reply from their own provisioned
 * account through Stalwart's real MTA into the intake mailbox the channel
 * polls. Speaks the wire protocol over TLS (self-signed accepted; the app
 * server runs with TLS_SKIP_VERIFY=1 for the same reason). The exchange is
 * strictly sequential: each step waits for the server line it needs before
 * sending the next command.
 */
async function smtpSend(
	port: number,
	username: string,
	password: string,
	from: string,
	to: string,
	rawMessage: string
): Promise<void> {
	const socket = tls.connect({ host: "127.0.0.1", port, rejectUnauthorized: false });
	socket.setTimeout(15_000);

	let buffer = "";
	let failed: Error | null = null;
	socket.on("error", (err) => {
		failed = err;
	});

	const readLine = async (): Promise<string> => {
		while (true) {
			const idx = buffer.indexOf("\r\n");
			if (idx >= 0) {
				const line = buffer.slice(0, idx);
				buffer = buffer.slice(idx + 2);
				return line;
			}
			if (failed) throw failed;
			await new Promise<void>((resolve, reject) => {
				const onData = (chunk: Buffer) => {
					buffer += chunk.toString("utf8");
					socket.off("data", onData);
					socket.off("close", onClose);
					socket.off("timeout", onTimeout);
					resolve();
				};
				const onClose = () => {
					socket.off("data", onData);
					socket.off("timeout", onTimeout);
					reject(new Error("SMTP connection closed"));
				};
				const onTimeout = () => {
					socket.off("data", onData);
					socket.off("close", onClose);
					reject(new Error("SMTP timed out waiting for data"));
				};
				socket.once("data", onData);
				socket.once("close", onClose);
				socket.once("timeout", onTimeout);
			});
		}
	};

	const write = (text: string) => socket.write(text);
	const expectCode = (prefix: string, what: string): Promise<string> =>
		readLine().then((line) => {
			if (failed) throw failed;
			if (!line.startsWith(prefix)) {
				throw new Error(`${what}: ${line}`);
			}
			return line;
		});

	try {
		await expectCode("220", "greeting");
		write("EHLO wire-loop.test\r\n");
		// EHLO replies are multiline: `250-...` until a final `250 ...`.
		for (;;) {
			const line = await readLine();
			if (failed) throw failed;
			if (line.startsWith("250 ")) break;
			if (!line.startsWith("250-")) throw new Error(`EHLO: ${line}`);
		}
		const auth = Buffer.from(`\0${username}\0${password}`).toString("base64");
		write(`AUTH PLAIN ${auth}\r\n`);
		await expectCode("235", "AUTH PLAIN");
		write(`MAIL FROM:<${from}>\r\n`);
		await expectCode("250", "MAIL FROM");
		write(`RCPT TO:<${to}>\r\n`);
		await expectCode("250", "RCPT TO");
		write("DATA\r\n");
		await expectCode("354", "DATA");
		write(`${rawMessage}\r\n.\r\n`);
		await expectCode("250", "message body");
		write("QUIT\r\n");
		await expectCode("221", "QUIT");
	} finally {
		socket.destroy();
	}
}

test.describe("Email ↔ portal wire loop (WIRE_MAILLOOP)", () => {
	test.describe.configure({ mode: "serial" });

	// Runs only when the runner is started with WIRE_MAILLOOP=1, which spawns
	// the local Stalwart fixture (IMAPS + SMTP) and starts the app server with
	// TLS_SKIP_VERIFY=1 for the intake channel's self-signed certificate.
	test("portal submit → agent comment → SMTP notification → emailed reply appends via real IMAPS intake", async ({
		page,
		request,
		mail,
		playwright,
	}) => {
		const suffix = `${Date.now()}-wire`;
		const title = `Wire ticket ${suffix}`;
		const portal: PortalChannelHandle = await createPortalChannel(request, {
			slug: `wire-portal-${suffix}`,
			name: `Wire Portal ${suffix}`,
		});
		const ws = await createWorkspaceViaAPI(request, generateWorkspace(`wire-ws-${suffix}`));

		const itemTypesResp = await request.get("/api/v2/item-types");
		expect(itemTypesResp.status()).toBe(200);
		const itemTypesDoc = await itemTypesResp.json();

		// Intake channel: polls the Stalwart INBOX over IMAPS and is linked to
		// the portal so its tickets surface there.
		const createResp = await request.post("/api/channels", {
			data: {
				name: `Wire intake ${suffix}`,
				type: "email",
				direction: "inbound",
				status: "disabled",
			},
		});
		expect(createResp.status()).toBe(201);
		const created = await createResp.json();
		const intakeChannelID: number = created.id;
		const configResp = await request.put(`/api/channels/${intakeChannelID}/config`, {
			data: {
				config: {
					email_auth_method: "basic",
					imap_host: "127.0.0.1",
					imap_port: STALWART_IMAPS,
					imap_encryption: "ssl",
					imap_username: STALWART_IMAP_USERNAME,
					imap_password: STALWART_IMAP_PASSWORD,
					email_workspace_id: ws.id,
					email_item_type_id: itemTypesDoc.data[0].id,
					email_mailbox: "INBOX",
					email_connected_portal_id: portal.channelId,
				},
			},
		});
		expect(configResp.status(), await configResp.text()).toBe(200);
		const enableResp = await request.put(`/api/channels/${intakeChannelID}/toggle`);
		expect(enableResp.status()).toBe(200);

		// The customer signs in via the real magic-link flow — which itself
		// arrives over SMTP into Mailpit. Open registration provisions the
		// customer on first request.
		const magicResp = await request.post(`/api/portal/${portal.slug}/auth/request`, {
			data: { email: CUSTOMER_ADDRESS },
		});
		expect(magicResp.status()).toBeLessThan(300);
		const magicLink = mail.extractLink(
			(
				await mail.waitForLast({
					to: CUSTOMER_ADDRESS,
					subject: "Sign in to your portal",
					timeoutMs: 15_000,
				})
			).Text,
			/https?:\/\/\S+\/portal\/\S+\/verify#token=[^\s>]+/
		);
		expect(magicLink).toBeTruthy();
		const token = magicLink.match(/#token=([^\s>]+)/)![1];
		const verifyResp = await request.get(
			`/api/portal/${portal.slug}/auth/verify?token=${encodeURIComponent(token)}`
		);
		expect(verifyResp.status()).toBeLessThan(300);
		const setCookie = verifyResp
			.headersArray()
			.filter((h) => h.name.toLowerCase() === "set-cookie")
			.map((h) => h.value.split(";")[0])
			.find((v) => v.length > 0);
		expect(setCookie, "session cookie from magic-link verification").toBeTruthy();
		const customerHeaders = { Cookie: setCookie!, ...SEC_FETCH };

		// The customer creates the ticket through the production submission
		// endpoint; creation mints the synthetic thread anchor.
		const submitResp = await request.post(`/api/portal/${portal.slug}/submit`, {
			headers: customerHeaders,
			data: { title, description: "Submitted from the portal." },
		});
		expect(submitResp.status()).toBe(201);
		const itemID: number = (await submitResp.json()).item_id;

		// The agent replies publicly; the durable consumer enqueues the
		// customer notification for real SMTP delivery.
		const commentResp = await request.post(`/api/v2/items/${itemID}/comments`, {
			data: { content: "Parts ordered; we will update you here." },
		});
		expect(commentResp.status()).toBe(201);

		// The notification must genuinely arrive in the customer's mailbox:
		// wait for it over the wire (delivered by the default SMTP channel)
		// and take its Message-ID as the In-Reply-To of the customer's reply.
		const delivered = await mail.waitForLast({
			to: CUSTOMER_ADDRESS,
			subject: `Re: ${title}`,
			timeoutMs: 20_000,
		});
		const notificationMessageID = `<${(delivered as unknown as { MessageID: string }).MessageID}>`;
		expect(notificationMessageID).toContain("@");

		// The customer replies from their own provisioned account over real
		// SMTPS; Stalwart delivers it into the intake mailbox the channel polls.
		const replyMessageID = `<wire-reply-${suffix}@customer.test>`;
		const reply = [
			`From: Loop Customer <${CUSTOMER_ADDRESS}>`,
			`To: Support <${STALWART_IMAP_USERNAME}>`,
			`Subject: Re: ${title}`,
			`Message-ID: ${replyMessageID}`,
			`In-Reply-To: ${notificationMessageID}`,
			`References: <ws-item-${itemID}@windshift.test> ${notificationMessageID}`,
			`Date: ${new Date().toUTCString()}`,
			"Content-Type: text/plain; charset=utf-8",
			"",
			"Tested from the wire — all good now.",
			"",
		].join("\r\n");
		await smtpSend(
			STALWART_SMTPS,
			STALWART_CUSTOMER_USERNAME,
			STALWART_CUSTOMER_PASSWORD,
			CUSTOMER_ADDRESS,
			STALWART_IMAP_USERNAME,
			reply
		);

		// Intake runs on the production path: trigger the channel poll and
		// wait for the customer's reply to appear on the portal ticket.
		const processDeadline = Date.now() + 30_000;
		let replied = false;
		while (Date.now() < processDeadline && !replied) {
			const processResp = await request.post(
				`/api/channels/${intakeChannelID}/process-emails`
			);
			expect(processResp.status()).toBe(200);
			const commentsResp = await request.get(
				`/api/portal/${portal.slug}/requests/${itemID}/comments`,
				{ headers: customerHeaders }
			);
			expect(commentsResp.status()).toBe(200);
			const comments = await commentsResp.json();
			replied = (comments ?? []).some((c: { content?: string }) =>
				(c.content ?? "").includes("Tested from the wire")
			);
			if (!replied) await new Promise((r) => setTimeout(r, 500));
		}
		expect(replied, "emailed reply appended to the portal ticket").toBe(true);

		// The same reply is observable in the real Stalwart mailbox over JMAP,
		// proving the intake consumed a genuine IMAPS message rather than a mock.
		const observed = await waitForStalwartMessage(playwright, {
			username: STALWART_IMAP_USERNAME,
			password: STALWART_IMAP_PASSWORD,
			subject: `Re: ${title}`,
			timeoutMs: 10_000,
		});
		expect(observed.text).toContain("Tested from the wire");

		// The customer signs in through the portal UI (magic link over the
		// wire again) and sees the whole conversation on the ticket page.
		// clearCookies discards the admin storageState so the portal renders
		// its signed-out sign-in entry point.
		await page.context().clearCookies();
		await page.goto(`/portal/${portal.slug}`);
		await page.locator("#portal-sign-in").click();
		await signInViaMagicLink(page, {
			mail,
			slug: portal.slug,
			email: CUSTOMER_ADDRESS,
		});

		await page.goto(`/portal/${portal.slug}?view=requests&id=${itemID}`);
		await expect(page.getByTestId("portal-request-detail-title")).toHaveText(title);
		await expect(page.getByTestId("portal-request-comment")).toHaveCount(2);
		await expect(page.getByTestId("portal-request-comment").last()).toContainText(
			"Tested from the wire"
		);
	});
});
