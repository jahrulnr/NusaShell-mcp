const accounts = [{
  id: "work",
  name: "Studio",
  email: "hello@studio.example",
  username: "hello@studio.example",
  enabled: true,
  hasCredential: true,
  imap: { host: "imap.studio.example", port: 993, secure: true, starttls: false, rejectUnauthorized: true },
  smtp: { host: "smtp.studio.example", port: 465, secure: true, starttls: false, rejectUnauthorized: true },
}];

const messages = [
  {
    accountId: "work",
    mailboxId: "INBOX",
    uid: 103,
    subject: "Review notes for the new correspondence desk",
    from: { name: "Maya Chen", address: "maya@example.com" },
    to: [{ name: "Studio", address: "hello@studio.example" }],
    date: new Date().toISOString(),
    unread: true,
    starred: true,
    hasAttachments: true,
    excerpt: "The navigation feels much clearer now. I left three focused notes on the reading pane.",
  },
  {
    accountId: "work",
    mailboxId: "INBOX",
    uid: 102,
    subject: "Thursday project handoff",
    from: { name: "Rafi", address: "rafi@example.com" },
    to: [{ address: "hello@studio.example" }],
    date: "2026-07-29T10:30:00Z",
    unread: false,
    starred: false,
    hasAttachments: false,
    excerpt: "Everything is ready for the client walkthrough on Thursday afternoon.",
  },
  {
    accountId: "work",
    mailboxId: "INBOX",
    uid: 101,
    subject: "Invoice and July usage report",
    from: { name: "Accounts", address: "billing@example.com" },
    to: [{ address: "hello@studio.example" }],
    date: "2026-07-28T08:00:00Z",
    unread: false,
    starred: false,
    hasAttachments: true,
    excerpt: "Attached are the invoice and usage summary for July.",
  },
];

window.shell = {
  mailAccounts: {
    list: async () => ({ accounts, canPersistCredentials: true }),
    save: async () => ({ accounts, canPersistCredentials: true }),
    delete: async () => ({ accounts: [], canPersistCredentials: true }),
  },
  callTool: async (_pluginId, name, args) => {
    const data = name === "mailboxes"
      ? {
          accountId: "work",
          mailboxes: [
            { id: "INBOX", name: "Inbox", specialUse: "\\Inbox", total: 18, unread: 4 },
            { id: "Archive", name: "Archive", specialUse: "\\Archive", total: 92, unread: 0 },
            { id: "Sent", name: "Sent", specialUse: "\\Sent", total: 31, unread: 0 },
          ],
        }
      : name === "read"
        ? {
            message: {
              ...messages.find((message) => message.uid === args.uid),
              bodyText: "Hi Studio,\n\nThe navigation feels much clearer now. I left three focused notes on the reading pane and account setup.\n\nBest,\nMaya",
              bodyHtml: `
                <div style="max-width:560px;margin:auto">
                  <div style="display:flex;align-items:center;gap:12px;margin-bottom:24px">
                    <div style="width:38px;height:38px;display:grid;place-items:center;border-radius:8px;background:#173f66;color:#dceeff;font-weight:700">G</div>
                    <div><strong>Google</strong><br><small style="color:#627083">to hello@studio.example</small></div>
                  </div>
                  <h2 style="font-size:18px">App password created</h2>
                  <p>This formatted mail is rendered inside an isolated document.</p>
                  <p><a href="https://example.com/account">Review account activity</a></p>
                </div>`,
              attachments: [{ filename: "review-notes.pdf", contentType: "application/pdf", size: 184320 }],
            },
          }
        : { messages, total: messages.length, accountCursors: { work: null } };
    return { structuredContent: data };
  },
};
