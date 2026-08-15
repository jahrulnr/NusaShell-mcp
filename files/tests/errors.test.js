import { describe, it, expect } from "vitest";
import { z } from "zod";
import { safeFilesError } from "../mcp/errors.js";

describe("safeFilesError", () => {
  it("surfaces Zod field path and max messages so agents can self-correct", () => {
    const schema = z.object({
      after: z.number().int().min(0).max(10),
    });
    let error;
    try {
      schema.parse({ after: 12 });
    } catch (e) {
      error = e;
    }
    const message = safeFilesError(error);
    expect(message).toContain("Files tool input is invalid");
    expect(message).toContain("after");
    expect(message).toMatch(/<=\s*10|maximum|Too big/i);
  });

  it("passes through ordinary Error messages", () => {
    expect(safeFilesError(new Error("File not found"))).toBe("File not found");
  });
});
