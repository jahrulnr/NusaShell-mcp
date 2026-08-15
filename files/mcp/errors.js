/**
 * Turn thrown values into short agent-safe strings.
 * Zod validation failures used to collapse to a useless generic line, so models
 * retry the same invalid args (e.g. after=12 when max is 10). Surface field
 * path + message without dumping full stacks.
 */
export function safeFilesError(error) {
  if (error && typeof error === "object" && "issues" in error && Array.isArray(error.issues)) {
    const detail = error.issues
      .slice(0, 5)
      .map((issue) => {
        const path = Array.isArray(issue.path) && issue.path.length > 0
          ? issue.path.join(".")
          : "(root)";
        const message = typeof issue.message === "string" ? issue.message : "invalid";
        return `${path}: ${message}`;
      })
      .join("; ");
    return detail ? `Files tool input is invalid (${detail})` : "Files tool input is invalid";
  }

  const message = error instanceof Error ? error.message : String(error);

  return message
    .replace(/[\u0000-\u001f\u007f]+/g, " ")
    .slice(0, 1000);
}
