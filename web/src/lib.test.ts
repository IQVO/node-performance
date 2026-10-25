import { describe, expect, it } from "vitest";
import { minutes, nodeIdProblem, pct, utcLabel } from "./lib";

describe("utcLabel", () => {
  it("formats an RFC 3339 instant as UTC seconds", () => {
    expect(utcLabel("2026-10-22T14:03:00Z")).toBe("2026-10-22 14:03:00Z");
  });

  it("keeps the raw text when unparseable and a placeholder when absent", () => {
    expect(utcLabel("not a date")).toBe("not a date");
    expect(utcLabel(undefined)).not.toBe("");
  });
});

describe("pct", () => {
  it("renders a rate as a one-decimal percentage", () => {
    expect(pct(0.984)).toBe("98.4%");
    expect(pct(0)).toBe("0.0%");
    expect(pct(1)).toBe("100.0%");
  });
});

describe("minutes", () => {
  it("renders whole minutes", () => {
    expect(minutes(38)).toBe("38 min");
    expect(minutes(0)).toBe("0 min");
  });
});

describe("nodeIdProblem", () => {
  it("accepts a well-formed id", () => {
    expect(nodeIdProblem("N-0042")).toBeNull();
  });

  it("rejects an empty, overlong or malformed id the way the domain does", () => {
    expect(nodeIdProblem("")).toMatch(/Enter a node id/);
    expect(nodeIdProblem("x".repeat(65))).toMatch(/at most 64 characters/);
    expect(nodeIdProblem("N 42")).toMatch(/cannot contain spaces/);
    expect(nodeIdProblem("N/42")).toMatch(/cannot contain spaces/);
  });
});
