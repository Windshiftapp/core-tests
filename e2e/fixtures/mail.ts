import { type APIRequestContext, test as base, expect } from './context-path';

/**
 * Mailpit email-capture fixture.
 *
 * `run-e2e.sh` launches Mailpit on a free SMTP port + HTTP port when the
 * binary is on PATH and exposes them via $MAILPIT_SMTP_PORT and
 * $MAILPIT_HTTP_PORT. `global.setup.ts` then points Windshift's default
 * outbound SMTP channel at Mailpit and toggles it to 'enabled'.
 *
 * Specs use `mail.waitForLast({ to, subject, timeoutMs })` to poll until a
 * matching message arrives, plus `mail.extractLink(html, regex)` to pull a
 * link out of the message body (e.g. magic-link verification URLs).
 *
 * If $MAILPIT_HTTP_PORT is not set during an ad-hoc local run, call
 * `mail.skipIfMissing()` at the top of email-using tests. CI sets
 * E2E_REQUIRE_MAILPIT=1, which makes the same condition a hard failure.
 */

export interface MailpitMessage {
  ID: string;
  From: { Name: string; Address: string };
  To: Array<{ Name: string; Address: string }>;
  Subject: string;
  Snippet: string;
  Created: string;
}

export interface MailpitMessageDetail extends MailpitMessage {
  HTML: string;
  Text: string;
}

interface MailFixtures {
  mail: {
    available: boolean;
    skipIfMissing: () => void;
    listMessages: () => Promise<MailpitMessage[]>;
    getMessage: (id: string) => Promise<MailpitMessageDetail>;
    waitForLast: (opts: {
      to?: string | RegExp;
      subject?: string | RegExp;
      since?: Date;
      timeoutMs?: number;
    }) => Promise<MailpitMessageDetail>;
    extractLink: (body: string, pattern: RegExp) => string;
    deleteAll: () => Promise<void>;
  };
}

export const test = base.extend<MailFixtures>({
  mail: async ({ playwright }, use, testInfo) => {
    const httpPort = process.env.MAILPIT_HTTP_PORT;
    const baseURL = httpPort ? `http://127.0.0.1:${httpPort}` : '';
    const available = !!httpPort;
    const required = process.env.E2E_REQUIRE_MAILPIT === '1';

    let api: APIRequestContext | null = null;
    if (available) {
      api = await playwright.request.newContext({ baseURL });
    }

    const requireApi = (): APIRequestContext => {
      if (!api) {
        throw new Error(
          'Mailpit is not available. Either install mailpit on PATH or call mail.skipIfMissing() before using email helpers.'
        );
      }
      return api;
    };

    const matchAddr = (addrs: Array<{ Address: string }>, m: string | RegExp): boolean => {
      const re = typeof m === 'string' ? new RegExp(m, 'i') : m;
      return addrs.some((a) => re.test(a.Address));
    };
    const matchString = (s: string, m: string | RegExp): boolean => {
      const re = typeof m === 'string' ? new RegExp(m) : m;
      return re.test(s);
    };

    await use({
      available,

      skipIfMissing: () => {
        if (required && !available) {
          throw new Error(
            'Mailpit is required for this run, but MAILPIT_HTTP_PORT is not configured.'
          );
        }
        testInfo.skip(!available, 'Mailpit not available (install mailpit on PATH)');
      },

      listMessages: async () => {
        const resp = await requireApi().get(`${baseURL}/api/v1/messages`);
        expect(resp.ok(), `mailpit list failed: ${resp.status()}`).toBeTruthy();
        const body = await resp.json();
        return (body.messages ?? []) as MailpitMessage[];
      },

      getMessage: async (id) => {
        const resp = await requireApi().get(`${baseURL}/api/v1/message/${id}`);
        expect(resp.ok(), `mailpit get failed: ${resp.status()}`).toBeTruthy();
        return (await resp.json()) as MailpitMessageDetail;
      },

      waitForLast: async ({ to, subject, since, timeoutMs = 5000 }) => {
        const ctx = requireApi();
        const deadline = Date.now() + timeoutMs;
        const sinceMs = since?.getTime() ?? 0;
        let lastErr = '';
        while (Date.now() < deadline) {
          const resp = await ctx.get(`${baseURL}/api/v1/messages`);
          if (!resp.ok()) {
            lastErr = `list ${resp.status()}`;
            await new Promise((r) => setTimeout(r, 200));
            continue;
          }
          const body = await resp.json();
          const messages = (body.messages ?? []) as MailpitMessage[];
          const match = messages.find((m) => {
            if (to && !matchAddr(m.To, to)) return false;
            if (subject && !matchString(m.Subject, subject)) return false;
            if (sinceMs && new Date(m.Created).getTime() < sinceMs) return false;
            return true;
          });
          if (match) {
            const detailResp = await ctx.get(`${baseURL}/api/v1/message/${match.ID}`);
            expect(detailResp.ok()).toBeTruthy();
            return (await detailResp.json()) as MailpitMessageDetail;
          }
          await new Promise((r) => setTimeout(r, 200));
        }
        throw new Error(
          `No matching email within ${timeoutMs}ms (to=${String(to)} subject=${String(
            subject
          )}; lastErr=${lastErr})`
        );
      },

      extractLink: (body, pattern) => {
        const m = body.match(pattern);
        if (!m) {
          throw new Error(`link not found; pattern=${pattern} body-snippet=${body.slice(0, 200)}`);
        }
        return m[0];
      },

      deleteAll: async () => {
        const resp = await requireApi().delete(`${baseURL}/api/v1/messages`);
        expect(resp.ok()).toBeTruthy();
      },
    });

    if (api) await api.dispose().catch(() => {});
  },
});

export { expect };
