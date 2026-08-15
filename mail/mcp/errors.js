export function safeMailError(error, sensitiveValues = []) {
  if (error && typeof error === "object" && "issues" in error) {
    return "Mail tool input is invalid";
  }

  const message = error instanceof Error ? error.message : String(error);
  const responseText = typeof error?.responseText === "string"
    ? error.responseText.trim()
    : "";
  const detailedMessage = message === "Command failed" && responseText
    ? `Mail server rejected the command: ${responseText}`
    : message;

  return sensitiveValues.reduce(redactLiteral, detailedMessage)
    .replace(/[\u0000-\u001f\u007f]+/g, " ")
    .replace(/:\/\/[^/@\s]+@/g, "://[REDACTED]@")
    .replace(/(pass(?:word)?|token|secret)\s*[=:]\s*\S+/gi, "$1=[REDACTED]")
    .slice(0, 1000);
}

function redactLiteral(message, value) {
  return value ? message.split(value).join("[REDACTED]") : message;
}
