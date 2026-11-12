import * as fs from "node:fs";
import * as path from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test as setup } from "./fixtures/context-path";

/**
 * Global setup that runs once before all tests
 * Completes application setup and creates authenticated session
 */

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
// E2E_AUTH_FILE lets run-e2e.sh point each concurrent run at its own file.
const authFile =
	process.env.E2E_AUTH_FILE ?? path.join(__dirname, ".auth/user.json");

setup(
	"complete application setup and authenticate",
	async ({ page, request }) => {
		const baseURL = process.env.BASE_URL || "http://localhost:8080";

		// Step 1: Check if setup is already completed
		const statusResponse = await request.get(`${baseURL}/api/setup/status`);
		expect(statusResponse.ok()).toBeTruthy();

		const setupStatus = await statusResponse.json();

		if (!setupStatus.setup_completed) {
			console.log(
				"🔧 Application setup not completed, running initial setup...",
			);

			// Complete initial setup
			const setupResponse = await request.post(
				`${baseURL}/api/setup/complete`,
				{
					headers: {
						"Sec-Fetch-Site": "same-origin",
					},
					data: {
						admin_user: {
							email: "admin@e2etest.com",
							username: "admin",
							password: "TestPass123!", // Plaintext — hashed server-side
							first_name: "E2E",
							last_name: "Admin",
						},
						module_settings: {
							time_tracking_enabled: true,
							test_management_enabled: true,
						},
					},
				},
			);

			expect(setupResponse.ok()).toBeTruthy();
			console.log("✅ Application setup completed successfully");
		} else {
			console.log("✅ Application setup already completed");
		}

		// Step 2: Login via API to get session cookie
		console.log("🔐 Logging in to create authenticated session...");

		const loginResponse = await request.post(`${baseURL}/api/auth/login`, {
			data: {
				email_or_username: "admin",
				password: "TestPass123!",
				remember_me: false,
			},
		});

		expect(loginResponse.ok()).toBeTruthy();

		console.log("✅ Authentication successful");

		// Step 3: Configure outbound SMTP channel to point at Mailpit (if running).
		//   The initial DB seed (database.go:1216) creates a default outbound SMTP
		//   channel named "Notification Mail" with empty config and status='pending'.
		//   When Mailpit is running (run-e2e.sh sets $MAILPIT_SMTP_PORT), we PUT
		//   the channel config + toggle status to 'enabled'. Ad-hoc local runs may
		//   omit Mailpit; strict CI runs fail immediately when the env var is absent.
		const mailpitSmtpPort = process.env.MAILPIT_SMTP_PORT;
		if (process.env.E2E_REQUIRE_MAILPIT === "1" && !mailpitSmtpPort) {
			throw new Error(
				"Mailpit is required for this run, but MAILPIT_SMTP_PORT is not configured.",
			);
		}
		if (mailpitSmtpPort) {
			console.log(
				`📬 Configuring SMTP channel → Mailpit at 127.0.0.1:${mailpitSmtpPort}...`,
			);
			const sec = { "Sec-Fetch-Site": "same-origin" };
			const channelsResp = await request.get(`${baseURL}/api/channels`, {
				headers: sec,
			});
			expect(channelsResp.ok(), "list channels failed").toBeTruthy();
			const channelsBody = await channelsResp.json();
			const channels: Array<{
				id: number;
				name: string;
				type: string;
				direction: string;
				is_default?: boolean;
				status: string;
				description: string;
				category_id?: number | null;
			}> = channelsBody.data ?? channelsBody;
			const smtpChannel = channels.find(
				(c) => c.type === "smtp" && c.direction === "outbound",
			);
			if (!smtpChannel) {
				throw new Error("No outbound SMTP channel found in seeded channels");
			}

			const newConfig = {
				smtp_host: "127.0.0.1",
				smtp_port: Number(mailpitSmtpPort),
				smtp_username: "",
				smtp_password: "",
				smtp_from_email: "e2e@windshift.test",
				smtp_from_name: "Windshift E2E",
				smtp_encryption: "none",
			};

			const updateResp = await request.put(
				`${baseURL}/api/channels/${smtpChannel.id}/config`,
				{
					headers: sec,
					data: { config: newConfig },
				},
			);
			expect(
				updateResp.ok(),
				`channel config update failed: ${updateResp.status()} ${await updateResp.text()}`,
			).toBeTruthy();

			// Toggle: 'pending'/'disabled' → 'enabled'. If already enabled (re-run on a
			// pre-existing DB), toggling would flip back to disabled — guard against that.
			if (smtpChannel.status !== "enabled") {
				const toggleResp = await request.put(
					`${baseURL}/api/channels/${smtpChannel.id}/toggle`,
					{ headers: sec },
				);
				expect(
					toggleResp.ok(),
					`channel toggle failed: ${toggleResp.status()} ${await toggleResp.text()}`,
				).toBeTruthy();
			}
			console.log("✅ SMTP channel configured + enabled");
		} else {
			console.log(
				"ℹ️  MAILPIT_SMTP_PORT not set — skipping SMTP channel config",
			);
		}

		// Step 4: Establish browser auth through the browser context's request
		// client. It shares the page's cookie jar and network identity, unlike the
		// standalone request fixture used for setup administration above.
		await page.context().clearCookies();
		const browserLoginResponse = await page.context().request.post(
			`${baseURL}/api/auth/login`,
			{
				data: {
					email_or_username: "admin",
					password: "TestPass123!",
					remember_me: false,
				},
			},
		);
		expect(browserLoginResponse.ok()).toBeTruthy();
		await page.goto(`${baseURL}/`);
		await expect(page.getByTestId("login-dialog")).not.toBeVisible({
			timeout: 10000,
		});

		fs.mkdirSync(path.dirname(authFile), { recursive: true });
		await page.context().storageState({ path: authFile });
		console.log("💾 Authentication state saved for reuse");
	},
);
