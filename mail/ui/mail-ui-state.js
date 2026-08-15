export function chooseInitialAccountId(accounts, selectedAccountId) {
  const selected = accounts.find(
    (account) => account.id === selectedAccountId && account.enabled,
  );
  return selected?.id ?? accounts.find((account) => account.enabled)?.id ?? null;
}

export function readableMailError(error) {
  const message = (error instanceof Error ? error.message : String(error))
    .replace(/^Error invoking remote method '[^']+':\s*/i, "");
  if (/application-specific password required/i.test(message)) {
    return "Gmail rejected this credential. Edit the account and replace it with a Google App Password; a regular Google account password will not work.";
  }
  return message;
}

export const EMAIL_FRAME_SANDBOX = "allow-same-origin";

export function preferredMailBody(message) {
  const html = typeof message?.bodyHtml === "string" ? message.bodyHtml.trim() : "";
  if (html) return { kind: "html", value: html };
  const text = typeof message?.bodyText === "string" ? message.bodyText.trim() : "";
  return {
    kind: "text",
    value: text || "This message has no readable body.",
  };
}

export function mailFrameDocument(html) {
  return `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="referrer" content="no-referrer">
  <meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'none'; connect-src 'none'; object-src 'none'; frame-src 'none'; media-src 'none'; font-src 'none'; form-action 'none'; base-uri 'none'; img-src https: data:; style-src 'unsafe-inline'">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <style>
    :root { color-scheme: light; }
    * { box-sizing: border-box; }
    html, body { min-width: 0; margin: 0; background: #fff; color: #172033; }
    body { padding: 24px; font: 14px/1.6 Arial, Helvetica, sans-serif; overflow-wrap: anywhere; }
    img { max-width: 100% !important; height: auto !important; }
    table { max-width: 100% !important; }
    pre { max-width: 100%; overflow: auto; white-space: pre-wrap; }
    a { color: #175fbd; }
    blockquote { margin-inline: 0; padding-left: 14px; border-left: 3px solid #d8e1ec; color: #526173; }
  </style>
</head>
<body>${String(html ?? "")}</body>
</html>`;
}
