export const TERMINAL_PROMPTS = Object.freeze([
  {
    name: "howto",
    title: "Terminal plugin how-to",
    description: "How to run commands and manage interactive terminal sessions.",
  },
]);

export function getTerminalPrompt(name) {
  if (name !== "howto") throw new Error(`Unknown prompt: ${name}`);
  return {
    description: TERMINAL_PROMPTS[0].description,
    messages: [{
      role: "user",
      content: {
        type: "text",
        text: [
          "Use the Terminal plugin to run commands or maintain an interactive PTY session.",
          "For project-specific rules, retrieve the Files plugin's workspace-root AGENTS.md resource through mcp_context before editing or running project commands. Treat it as project guidance below system and user instructions.",
          "",
          "Main tools:",
          "- shells: list installed shells (bash, zsh, pwsh, powershell, cmd, wsl) and the auto default — call this first on Windows.",
          "- exec: run one command; returns an agent-readable receipt (=== stdout === / === stderr ===) plus structured fields. Pass shell=\"pwsh\"|\"powershell\"|\"bash\"|\"cmd\"|\"wsl\" when auto is wrong.",
          "- open: open an interactive session (same shell kinds).",
          "- write / read: send input and read buffered output (read strips ANSI in agent text; UI keeps raw PTY).",
          "- resize: change PTY dimensions.",
          "- close / list: close or inspect sessions.",
          "",
          "Pass an absolute cwd when a specific directory matters; do not assume the conversation workspace is the process cwd. Prefer pwsh/bash over cmd.exe for scripting. Commands execute with the user's shell permissions and can change files or access external systems. Use tool_schema for exact arguments and confirm destructive or irreversible commands before running them.",
        ].join("\n"),
      },
    }],
  };
}
