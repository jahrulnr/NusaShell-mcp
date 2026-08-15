import { describe, expect, it } from "vitest";
import { safeMailError } from "../mcp/errors.js";

describe("safeMailError", () => {
  it("includes the IMAP response text when the library only says Command failed", () => {
    const error = Object.assign(new Error("Command failed"), {
      responseText: "[AUTHENTICATIONFAILED] Invalid credentials",
    });

    expect(safeMailError(error)).toBe(
      "Mail server rejected the command: [AUTHENTICATIONFAILED] Invalid credentials",
    );
  });

  it("redacts explicitly supplied credentials", () => {
    expect(safeMailError(
      new Error("Login failed for password super-secret"),
      ["super-secret"],
    )).not.toContain("super-secret");
  });

  it("keeps server errors on one log-safe line", () => {
    const error = Object.assign(new Error("Command failed"), {
      responseText: "Rejected\r\nfor policy",
    });

    expect(safeMailError(error)).toBe(
      "Mail server rejected the command: Rejected for policy",
    );
  });
});
