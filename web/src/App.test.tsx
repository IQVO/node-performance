import { MemoryRouter, Route, Routes } from "react-router-dom";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import App from "./App";
import { json, mockApi } from "./test/fetchMock";
import { gradeAPage, nodeScorecards } from "./test/fixtures";

// The console mounts the remote under its own splat route; the remote's
// relative links must resolve against the mount point, not the full URL.
function renderUnderHost(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/node-performance/*" element={<App />} />
      </Routes>
    </MemoryRouter>,
  );
}

const ROUTES = {
  "GET /scorecards": json(gradeAPage()),
  "GET /scorecards/N-0042": json(nodeScorecards()),
};

describe("App", () => {
  it("redirects the mount point to the scorecard list", async () => {
    mockApi(ROUTES);
    renderUnderHost("/node-performance");
    expect(await screen.findByRole("heading", { name: "Scorecards by grade" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Scorecards" })).toHaveAttribute(
      "href",
      "/node-performance/scorecards",
    );
  });

  it("keeps the sub-nav at the mount point from a deep route", async () => {
    mockApi(ROUTES);
    renderUnderHost("/node-performance/nodes/N-0042");
    await screen.findByRole("heading", { name: "Node N-0042" });
    expect(screen.getByRole("link", { name: "Node lookup" })).toHaveAttribute(
      "href",
      "/node-performance/nodes",
    );
  });

  it("navigates from the list into a node's scorecards and back", async () => {
    mockApi(ROUTES);
    renderUnderHost("/node-performance/scorecards");
    const user = userEvent.setup();
    await user.click((await screen.findAllByRole("link", { name: "N-0042" }))[0]);
    await screen.findByRole("heading", { name: "Node N-0042" });
    await user.click(screen.getByRole("link", { name: "← Node lookup" }));
    expect(await screen.findByRole("heading", { name: "Look up a node" })).toBeInTheDocument();
  });
});
