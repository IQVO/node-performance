import { useState } from "react";
import { Link } from "react-router-dom";
import { Card, DataTable } from "@warehouse/ui-kit";
import { listScorecards } from "../api";
import { Form, FormRow, SelectField, SubmitButton } from "../components/formkit";
import { GradePill, PageHeader, Stack } from "../components/layout";
import { RequestView } from "../components/ProblemAlert";
import { useRequest } from "../hooks/useRequest";
import { pct, utcLabel } from "../lib";
import { GRADES, WINDOWS } from "../types";
import type { Grade, Scorecard, Window } from "../types";

const GRADE_OPTIONS = GRADES.map((g) => ({ value: g, label: g }));
const WINDOW_OPTIONS = WINDOWS.map((w) => ({ value: w, label: w }));

/**
 * The scorecards of one grade: GET /scorecards?grade=&window=&limit=&after=,
 * ordered by scorecardId ascending, one page at a time. The grade is required
 * by the contract (it defaults to A here); the window filter narrows the page
 * to one of the two windows. Paging follows the `after` keyset cursor; the
 * cursors already visited are kept so "Previous" walks back without the API
 * having to support it.
 */
export function ScorecardListScreen() {
  const [draft, setDraft] = useState<{ grade: Grade; window: "" | Window }>({ grade: "A", window: "" });
  const [applied, setApplied] = useState<{ grade: Grade; window: "" | Window }>({ grade: "A", window: "" });
  // cursors[i] is the cursor of page i+1 ("" = the first page).
  const [cursors, setCursors] = useState<string[]>([""]);
  const cursor = cursors[cursors.length - 1];
  const page = useRequest(
    () =>
      listScorecards({
        grade: applied.grade,
        window: applied.window || undefined,
        limit: 25,
        after: cursor || undefined,
      }),
    `scorecards|${applied.grade}|${applied.window}|${cursor}`,
  );

  const apply = () => {
    setApplied({ grade: draft.grade, window: draft.window });
    setCursors([""]);
  };

  return (
    <Stack gap={5}>
      <PageHeader
        title="Scorecards by grade"
        subtitle="node-performance · every node graded from observed facts, over a rolling 7-day and 30-day window"
      />
      <Card title="Filters">
        <Form label="Scorecard filters" onSubmit={apply}>
          <FormRow>
            <SelectField
              label="Grade"
              value={draft.grade}
              onChange={(v) => setDraft({ ...draft, grade: v as Grade })}
              options={GRADE_OPTIONS}
            />
            <SelectField
              label="Window"
              value={draft.window}
              onChange={(v) => setDraft({ ...draft, window: v as "" | Window })}
              options={WINDOW_OPTIONS}
              emptyLabel="Both windows"
            />
            <SubmitButton>Apply filters</SubmitButton>
          </FormRow>
        </Form>
      </Card>
      <Card
        title={`Grade ${applied.grade}${applied.window ? ` · ${applied.window}` : ""} · page ${cursors.length}`}
        actions={
          <div style={{ display: "flex", gap: "var(--wh-space-2)" }}>
            <SubmitButton
              type="button"
              secondary
              disabled={cursors.length === 1 || page.status === "loading"}
              onClick={() => setCursors(cursors.slice(0, -1))}
            >
              Previous page
            </SubmitButton>
            <SubmitButton
              type="button"
              secondary
              disabled={page.status !== "success" || !page.data.nextCursor}
              onClick={() => {
                if (page.status === "success" && page.data.nextCursor) {
                  setCursors([...cursors, page.data.nextCursor]);
                }
              }}
            >
              Next page
            </SubmitButton>
          </div>
        }
      >
        <RequestView
          state={page}
          what="scorecards"
          isEmpty={(p) => p.items.length === 0}
          empty={cursors.length === 1 ? `No scorecard currently holds the grade ${applied.grade}.` : "No more scorecards on this page."}
        >
          {(p) => <ScorecardTable scorecards={p.items} />}
        </RequestView>
      </Card>
    </Stack>
  );
}

function ScorecardTable({ scorecards }: { scorecards: Scorecard[] }) {
  return (
    <DataTable
      rowKey={(s) => s.scorecardId}
      rows={scorecards}
      columns={[
        {
          key: "node",
          header: "Node",
          // Relative link: `..` is the parent route (this remote's mount
          // point), so it works under the console's /node-performance/*
          // and standalone at /.
          render: (s) => <Link to={`../nodes/${encodeURIComponent(s.nodeId)}`}>{s.nodeId}</Link>,
        },
        { key: "window", header: "Window", render: (s) => s.window },
        { key: "grade", header: "Grade", render: (s) => <GradePill grade={s.grade} /> },
        { key: "state", header: "State", render: (s) => s.state },
        {
          key: "onTime",
          header: "On-time handover",
          align: "right",
          render: (s) => pct(s.metrics.onTimeHandoverRate),
        },
        {
          key: "observations",
          header: "Observations",
          align: "right",
          render: (s) => String(s.metrics.observations),
        },
        { key: "computed", header: "Computed", render: (s) => utcLabel(s.computedAt) },
      ]}
    />
  );
}
