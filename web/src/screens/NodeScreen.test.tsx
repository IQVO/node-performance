import { MemoryRouter, Route, Routes } from "react-router-dom";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { NodeLookupScreen, NodeRoute } from "./NodeScreen";
import { json, mockApi, problem } from "../test/fetchMock";
import {
  METRICS_PROVISIONAL,
  nodeScorecards,
  scorecard,
} from "../test/fixtures";

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/node-performance/*">
          <Route path="nodes" element={<NodeLookupScreen />} />
          <Route path="nodes/:nodeId" element={<NodeRoute />} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

describe("NodeLookupScreen", () => {
  it("navigates to the node's scorecards on submit", async () => {
    mockApi({ "GET /scorecards/N-0042": json(nodeScorecards()) });
    renderAt("/node-performance/nodes");
    const user = userEvent.setup();
    await user.type(screen.getByLabelText(/Node id/), "N-0042");
    await user.click(screen.getByRole("button", { name: "Show scorecards" }));
    expect(await screen.findByRole("heading", { name: "Node N-0042" })).toBeInTheDocument();
  });

  it("refuses a malformed id instead of calling the API", async () => {
    const api = mockApi({ "GET /scorecards/bad/id": json({}) });
    renderAt("/node-performance/nodes");
    const user = userEvent.setup();
    await user.type(screen.getByLabelText(/Node id/), "bad/id");
    expect(screen.getByRole("button", { name: "Show scorecards" })).toBeDisabled();
    expect(screen.getByText("A node id cannot contain spaces, control characters or '/'.")).toBeInTheDocument();
    expect(api.calls).toHaveLength(0);
  });
});

describe("NodeScreen", () => {
  it("renders both windows with their grades and metrics", async () => {
    mockApi({ "GET /scorecards/N-0042": json(nodeScorecards()) });
    renderAt("/node-performance/nodes/N-0042");
    expect(await screen.findByRole("heading", { name: "Node N-0042" })).toBeInTheDocument();
    expect(screen.getByText("The 7d window")).toBeInTheDocument();
    expect(screen.getByText("The 30d window")).toBeInTheDocument();
    expect(screen.getAllByText("Feed freshness (p95)")).toHaveLength(2);
    expect(screen.getByText("93.0%")).toBeInTheDocument();
    expect(screen.getByText("96.2%")).toBeInTheDocument();
    expect(screen.getByText("55 min")).toBeInTheDocument();
    expect(screen.getAllByText("2026-10-22 14:03:00Z")).toHaveLength(2);
  });

  it("shows a single window when only one exists", async () => {
    mockApi({
      "GET /scorecards/N-0042": json({
        nodeId: "N-0042",
        scorecards: [scorecard({ metrics: METRICS_PROVISIONAL, grade: "PROVISIONAL", state: "PROVISIONAL" })],
      }),
    });
    renderAt("/node-performance/nodes/N-0042");
    expect(await screen.findByText("The 7d window")).toBeInTheDocument();
    expect(screen.queryByText("The 30d window")).not.toBeInTheDocument();
    expect(screen.getByText("Only one window exists for this node so far.")).toBeInTheDocument();
  });

  it("renders the not-found problem of an unknown node", async () => {
    mockApi({
      "GET /scorecards/N-9999": problem(404, "not-found", "Not found", "No scorecard exists for node N-9999."),
    });
    renderAt("/node-performance/nodes/N-9999");
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Not found");
    expect(alert).toHaveTextContent("No scorecard exists for node N-9999.");
  });
});
