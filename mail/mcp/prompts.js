export const MAIL_PROMPTS = Object.freeze([
  {
    name: "howto",
    title: "Mail plugin how-to",
    description: "How to inspect configured mail accounts, mailboxes, and messages.",
  },
]);

export function getMailPrompt(name) {
  if (name !== "howto") throw new Error(`Unknown prompt: ${name}`);
  return {
    description: MAIL_PROMPTS[0].description,
    messages: [{
      role: "user",
      content: {
        type: "text",
        text: [
          "Use the Mail plugin to inspect configured mail accounts and read mail.",
          "",
          "Main tools:",
          "- accounts / account_get / account_test: inspect and test configured accounts.",
          "- mailboxes: list folders for an account.",
          "- inbox / messages: list messages with bounded result options.",
          "- search: search messages.",
          "- read: read one message.",
          "",
          "Credentials and account configuration are host-owned and injected at runtime; do not ask the user to put secrets in tool arguments. Use tool_schema for exact account, mailbox, paging, and search fields. Mail operations can contact external servers and may expose message content, so confirm the intended account before acting.",
        ].join("\n"),
      },
    }],
  };
}
