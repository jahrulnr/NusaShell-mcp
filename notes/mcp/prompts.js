export const NOTES_PROMPTS = Object.freeze([
  {
    name: "howto",
    title: "Notes plugin how-to",
    description: "How to create, find, read, update, and delete notes.",
  },
]);

export function getNotesPrompt(name) {
  if (name !== "howto") throw new Error(`Unknown prompt: ${name}`);
  return {
    description: NOTES_PROMPTS[0].description,
    messages: [{
      role: "user",
      content: {
        type: "text",
        text: [
          "Use the Notes plugin for persistent local notes.",
          "",
          "Main tools:",
          "- create: create a note with a title and body.",
          "- list: list saved notes.",
          "- get: read one note by id.",
          "- update: change a note's title or body.",
          "- delete: permanently remove a note.",
          "- search: find notes by text.",
          "",
          "Use tool_schema for the exact arguments and required fields. Notes are persisted by the plugin and are separate from the shell conversation history.",
        ].join("\n"),
      },
    }],
  };
}
