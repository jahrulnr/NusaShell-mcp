import { describe, expect, it } from "vitest";
import {
  chooseInitialAccountId,
  EMAIL_FRAME_SANDBOX,
  mailFrameDocument,
  preferredMailBody,
  readableMailError,
} from "../ui/mail-ui-state.js";

describe("Mail UI state", () => {
  it("selects the first enabled account when opening Mail", () => {
    expect(chooseInitialAccountId([
      { id: "disabled", enabled: false },
      { id: "gmail", enabled: true },
      { id: "work", enabled: true },
    ], null)).toBe("gmail");
  });

  it("keeps an existing enabled selection", () => {
    expect(chooseInitialAccountId([
      { id: "gmail", enabled: true },
      { id: "work", enabled: true },
    ], "work")).toBe("work");
  });

  it("does not select a disabled account", () => {
    expect(chooseInitialAccountId([
      { id: "disabled", enabled: false },
    ], null)).toBeNull();
  });

  it("turns Gmail's app-password rejection into a recovery instruction", () => {
    expect(readableMailError(new Error(
      "Error invoking remote method 'tool:call': Mail server rejected the command: Application-specific password required: https://support.google.com/accounts/answer/18583",
    ))).toBe(
      "Gmail rejected this credential. Edit the account and replace it with a Google App Password; a regular Google account password will not work.",
    );
  });

  it("keeps other provider errors while removing Electron's IPC wrapper", () => {
    expect(readableMailError(
      "Error invoking remote method 'tool:call': Connection timed out",
    )).toBe("Connection timed out");
  });

  it("prefers the formatted HTML alternative over its plain-text fallback", () => {
    expect(preferredMailBody({
      bodyText: "Plain fallback",
      bodyHtml: "<strong>Formatted message</strong>",
    })).toEqual({
      kind: "html",
      value: "<strong>Formatted message</strong>",
    });
  });

  it("builds an isolated mail document that cannot run scripts or forms", () => {
    const document = mailFrameDocument("<strong>Formatted message</strong>");

    expect(EMAIL_FRAME_SANDBOX).not.toMatch(/allow-scripts|allow-forms|allow-popups/);
    expect(document).toContain("script-src 'none'");
    expect(document).toContain("form-action 'none'");
    expect(document).toContain("connect-src 'none'");
    expect(document).toContain("img-src https: data:");
    expect(document).toContain("<strong>Formatted message</strong>");
  });
});
