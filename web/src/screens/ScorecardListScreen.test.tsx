import { MemoryRouter } from "react-router-dom";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { ScorecardListScreen } from "./ScorecardListScreen";
import { json, mockApi, problem } from "../test/fetchMock";
import { gradeAPage } from "../test/fixtures";

function renderScreen() {
  return render(
    <MemoryRouter>
      <ScorecardListScreen />
    </MemoryRouter>,
  );
}

describe("ScorecardListScreen", () => {
  it("loads the first page of the default grade and renders one row per scorecard", async () => {
    const api = mockApi({ "GET /scorecards": json(gradeAPage()) });
    renderScreen();
    expect(await screen.findAllByRole("link", { name: "N-0042" })).toHaveLength(2);
    expect(screen.getByRole("heading", { name: "Scorecards by grade" })).toBeInTheDocument();
    expect(screen.getByText("1874")).toBeInTheDocument();
    const call = api.to("GET /scorecards")[0];
    expect(call.query.get("grade")).toBe("A");
    expect(call.query.get("window")).toBeNull();
    expect(screen.getByText("On-time handover")).toBeInTheDocument();
    expect(screen.getAllByRole("link", { name: "N-0042" })).toHaveLength(2);
    expect(api.calls.every((c) => c.method === "GET")).toBe(true);
  });

  it("applies the grade and window filters only on submit", async () => {
    const api = mockApi({ "GET /scorecards": json(gradeAPage()) });
    renderScreen();
    await screen.findByText("1874");
    const user = userEvent.setup();
    await user.selectOptions(screen.getByLabelText("Grade"), "PROVISIONAL");
    await user.selectOptions(screen.getByLabelText("Window"), "30d");
    expect(api.to("GET /scorecards")).toHaveLength(1); // not yet: filters are a form
    await user.click(screen.getByRole("button", { name: "Apply filters" }));
    await waitFor(() => expect(api.to("GET /scorecards")).toHaveLength(2));
    const call = api.to("GET /scorecards")[1];
    expect(call.query.get("grade")).toBe("PROVISIONAL");
    expect(call.query.get("window")).toBe("30d");
  });

  it("pages forward on nextCursor and back again", async () => {
    mockApi({
      // Without a cursor: the full first page (nextCursor set). With one:
      // the last page, a single row and no cursor.
      "GET /scorecards": (call) =>
        call.query.get("after")
          ? json(gradeAPage({ items: [gradeAPage().items[2]], nextCursor: undefined }))
          : json(gradeAPage()),
    });
    renderScreen();
    await screen.findByRole("link", { name: "N-0051" });
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Next page" }));
    expect(await screen.findByText("Grade A · page 2")).toBeInTheDocument();
    expect(screen.queryAllByRole("link", { name: "N-0042" })).toHaveLength(0);
    expect(screen.getByRole("button", { name: "Next page" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Previous page" }));
    expect(await screen.findByText("Grade A · page 1")).toBeInTheDocument();
    expect(await screen.findAllByRole("link", { name: "N-0042" })).toHaveLength(2);
  });

  it("renders the problem of a failed page", async () => {
    mockApi({
      "GET /scorecards": problem(
        422,
        "validation-failed",
        "Validation failed",
        "grade must be one of A, B, C, D, PROVISIONAL.",
      ),
    });
    renderScreen();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Validation failed");
    expect(alert).toHaveTextContent("grade must be one of");
  });

  it("renders an empty note when the grade holds no scorecard", async () => {
    mockApi({ "GET /scorecards": json({ items: [] }) });
    renderScreen();
    expect(await screen.findByText("No scorecard currently holds the grade A.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Next page" })).toBeDisabled();
  });
});
