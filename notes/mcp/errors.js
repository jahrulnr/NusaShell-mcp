export function safeNotesError(error) {
  if (error && typeof error === "object" && "issues" in error) {
    return "Notes tool input is invalid";
  }

  const message = error instanceof Error ? error.message : String(error);

  return message
    .replace(/[\u0000-\u001f\u007f]+/g, " ")
    .slice(0, 1000);
}
