import { describe, expect, it } from "vitest";
import { resolvePerformanceApiBase } from "./config";

describe("resolvePerformanceApiBase", () => {
  it("is this context's own /api/node-performance under the runtime origin", () => {
    expect(resolvePerformanceApiBase({ apiOrigin: "http://localhost:8000" }, true)).toBe(
      "http://localhost:8000/api/node-performance",
    );
  });

  it("drops trailing slashes of the origin", () => {
    expect(resolvePerformanceApiBase({ apiOrigin: "http://kong.local///" }, false)).toBe(
      "http://kong.local/api/node-performance",
    );
  });

  it("falls back to the developer port outside production", () => {
    expect(resolvePerformanceApiBase({}, false)).toBe("http://localhost:8080");
  });

  it("refuses to guess in production", () => {
    expect(() => resolvePerformanceApiBase({}, true)).toThrow(/apiOrigin is required/);
    expect(() => resolvePerformanceApiBase({ apiOrigin: "" }, true)).toThrow(/apiOrigin is required/);
  });
});
