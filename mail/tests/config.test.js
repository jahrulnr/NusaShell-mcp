import { describe, expect, it } from "vitest";
import {
  publicAccount,
  resolveAccount,
} from "../mcp/config.js";

const account = {
  id: "work",
  name: "Work",
  email: "me@example.com",
  username: "me@example.com",
  password: "app-password",
  enabled: true,
  imap: { host: "imap.example.com", port: 993, secure: true },
  smtp: { host: "smtp.example.com", port: 465, secure: true },
};

describe("mail account configuration", () => {
  it("never includes credentials in public account output", () => {
    const result = publicAccount(account);

    expect(result).toEqual(expect.objectContaining({
      id: "work",
      name: "Work",
      email: "me@example.com",
    }));
    expect(result).not.toHaveProperty("password");
    expect(result).not.toHaveProperty("username");
  });

  it("does not resolve disabled accounts", () => {
    expect(() => resolveAccount([{ ...account, enabled: false }], "work"))
      .toThrow(/not enabled/i);
  });
});
