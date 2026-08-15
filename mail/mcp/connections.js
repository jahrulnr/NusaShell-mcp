import { ImapFlow } from "imapflow";
import nodemailer from "nodemailer";
import { resolveAccount } from "./config.js";
import { safeMailError } from "./errors.js";

/**
 * Owns lazy IMAP connections for the lifetime of one MCP process.
 */
export class MailConnectionManager {
  /**
   * @param {readonly import("zod").infer<typeof import("./config.js").AccountSchema>[]} accounts
   */
  constructor(accounts) {
    this.accounts = accounts;
    /** @type {Map<string, ImapFlow>} */
    this.imapClients = new Map();
  }

  reloadAccounts(accounts) {
    this.accounts = accounts;
  }

  dropAccount(accountId) {
    const existing = this.imapClients.get(accountId);
    if (existing) {
      this.imapClients.delete(accountId);
      try { existing.close(); } catch {}
    }
  }

  async getImapClient(accountId) {
    const existing = this.imapClients.get(accountId);
    if (existing?.usable) return existing;
    if (existing) {
      this.imapClients.delete(accountId);
      existing.close();
    }

    const account = resolveAccount(this.accounts, accountId);
    const client = new ImapFlow({
      host: account.imap.host,
      port: account.imap.port,
      secure: account.imap.secure,
      doSTARTTLS: account.imap.starttls,
      tls: { rejectUnauthorized: account.imap.rejectUnauthorized },
      auth: { user: account.username, pass: account.password },
      logger: false,
    });
    try {
      await client.connect();
    } catch (error) {
      client.close();
      throw new Error(safeMailError(error, [account.password]));
    }
    this.imapClients.set(accountId, client);
    return client;
  }

  async testAccount(accountId, scope = "both") {
    const account = resolveAccount(this.accounts, accountId);
    const [incoming, outgoing] = await Promise.all([
      scope === "outgoing" ? undefined : this.#testImap(account),
      scope === "incoming" ? undefined : this.#testSmtp(account),
    ]);
    return {
      accountId,
      ...(incoming ? { incoming } : {}),
      ...(outgoing ? { outgoing } : {}),
    };
  }

  async #testImap(account) {
    const client = new ImapFlow({
      host: account.imap.host,
      port: account.imap.port,
      secure: account.imap.secure,
      doSTARTTLS: account.imap.starttls,
      tls: { rejectUnauthorized: account.imap.rejectUnauthorized },
      auth: { user: account.username, pass: account.password },
      logger: false,
    });
    try {
      await client.connect();
      const folders = await client.list();
      return { ok: true, folders: folders.length };
    } catch (error) {
      return { ok: false, error: safeMailError(error, [account.password]) };
    } finally {
      try {
        await client.logout();
      } catch {
        client.close();
      }
    }
  }

  async #testSmtp(account) {
    const transport = nodemailer.createTransport({
      host: account.smtp.host,
      port: account.smtp.port,
      secure: account.smtp.secure,
      requireTLS: account.smtp.starttls,
      tls: { rejectUnauthorized: account.smtp.rejectUnauthorized },
      auth: { user: account.username, pass: account.password },
      connectionTimeout: 15_000,
      greetingTimeout: 15_000,
      socketTimeout: 20_000,
    });
    try {
      await transport.verify();
      return { ok: true };
    } catch (error) {
      return { ok: false, error: safeMailError(error, [account.password]) };
    } finally {
      transport.close();
    }
  }

  async closeAll() {
    const clients = [...this.imapClients.values()];
    this.imapClients.clear();
    await Promise.allSettled(clients.map((client) => client.logout()));
  }
}
