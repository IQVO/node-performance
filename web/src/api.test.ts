import { afterEach, describe, expect, it } from "vitest";
import { getScorecard, listScorecards } from "./api";
import { json, mockApi, problem } from "./test/fetchMock";
import { gradeAPage, nodeScorecards } from "./test/fixtures";

afterEach(() => {
  // fetch is unstubbed by src/test/setup.ts; nothing else to clean here.
});

describe("listScorecards", () => {
  it("sends the required grade and the optional filters as query parameters", async () => {
    const api = mockApi({
      "GET /scorecards": json(gradeAPage()),
    });
    await listScorecards({ grade: "A", window: "7d", limit: 25, after: "N-0042:7d" });
    const call = api.to("GET /scorecards")[0];
    expect(call.path).toBe("/scorecards");
    expect(call.url).toContain("?");
    expect(call.query.get("grade")).toBe("A");
    expect(call.query.get("window")).toBe("7d");
    expect(call.query.get("limit")).toBe("25");
    expect(call.query.get("after")).toBe("N-0042:7d");
  });

  it("omits the absent optional filters entirely", async () => {
    const api = mockApi({ "GET /scorecards": json(gradeAPage({ nextCursor: undefined })) });
    const page = await listScorecards({ grade: "PROVISIONAL" });
    const call = api.to("GET /scorecards")[0];
    expect(call.query.get("window")).toBeNull();
    expect(call.query.get("limit")).toBeNull();
    expect(call.query.get("after")).toBeNull();
    expect(page.nextCursor).toBeUndefined();
    expect(page.items).toHaveLength(3);
  });

  it("normalizes an absent items array to an empty page", async () => {
    mockApi({ "GET /scorecards": json({}) });
    const page = await listScorecards({ grade: "D" });
    expect(page.items).toEqual([]);
  });
});

describe("getScorecard", () => {
  it("reads both windows of the node", async () => {
    mockApi({ "GET /scorecards/N-0042": json(nodeScorecards()) });
    const got = await getScorecard("N-0042");
    expect(got.nodeId).toBe("N-0042");
    expect(got.scorecards.map((s) => s.window)).toEqual(["7d", "30d"]);
  });

  it("encodes the node id into the path", async () => {
    const api = mockApi({ "GET /scorecards/N-0042": json(nodeScorecards()) });
    await getScorecard("N-0042");
    expect(api.to("GET /scorecards/N-0042")).toHaveLength(1);
  });

  it("surfaces the problem slug of a 404", async () => {
    mockApi({
      "GET /scorecards/N-9999": problem(404, "not-found", "Not found", "No scorecard exists for node N-9999."),
    });
    await expect(getScorecard("N-9999")).rejects.toMatchObject({
      slug: "not-found",
      status: 404,
      title: "Not found",
    });
  });

  it("surfaces a non-JSON error as a plain ApiError", async () => {
    mockApi({
      "GET /scorecards/N-0042": new Response("gateway exploded", { status: 502 }),
    });
    await expect(getScorecard("N-0042")).rejects.toMatchObject({ status: 502, slug: "" });
  });
});
