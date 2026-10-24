import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { Card } from "@warehouse/ui-kit";
import { ApiError, getScorecard } from "../api";
import { Facts, GradePill, MetricsTable, PageHeader, Stack } from "../components/layout";
import { Form, FormRow, SubmitButton, TextField } from "../components/formkit";
import { ProblemAlert, RequestView } from "../components/ProblemAlert";
import { useRequest } from "../hooks/useRequest";
import { nodeIdProblem, utcLabel } from "../lib";
import type { Scorecard } from "../types";

/** The route element of `nodes` (the lookup). */
export function NodeLookupScreen() {
  const [nodeId, setNodeId] = useState("");
  const navigate = useNavigate();
  const problem = nodeIdProblem(nodeId.trim());

  return (
    <Stack gap={5}>
      <PageHeader
        title="Look up a node"
        subtitle="node-performance · both scorecards of one node: its 7-day and its 30-day window"
      />
      <Card title="Node id">
        <Form
          label="Node lookup"
          onSubmit={() => {
            // Relative like every link here: ".." is the layout route (this
            // remote's mount point), so the detail URL is right under the
            // console's /node-performance/* and standalone at /.
            if (!problem) navigate(`../nodes/${encodeURIComponent(nodeId.trim())}`);
          }}
        >
          <FormRow>
            <TextField
              label="Node id"
              value={nodeId}
              onChange={setNodeId}
              placeholder="N-0042"
              required
              hint={problem ?? "The id the network's topology assigned, e.g. N-0042"}
            />
            <SubmitButton disabled={problem !== null}>Show scorecards</SubmitButton>
          </FormRow>
        </Form>
      </Card>
    </Stack>
  );
}

/** The route element of `nodes/:nodeId`. */
export function NodeRoute() {
  const { nodeId } = useParams();
  return nodeId ? <NodeScreen nodeId={nodeId} /> : null;
}

/**
 * One node's scorecards: GET /scorecards/{nodeId} -- the 7-day and the
 * 30-day window, in that order, each with its grade (or PROVISIONAL below
 * the 50-observation minimum) and its metrics. A node nothing was observed
 * about answers not-found.
 */
export function NodeScreen({ nodeId }: { nodeId: string }) {
  const node = useRequest(() => getScorecard(nodeId), `node|${nodeId}`);
  const problem = nodeIdProblem(nodeId);
  return (
    <Stack gap={5}>
      <PageHeader title={`Node ${nodeId}`} subtitle="Graded from observed facts only: what was asked of the node and what it did" />
      {problem ? (
        <ProblemAlert error={new ApiError(0, { detail: problem }, "Invalid node id")} />
      ) : (
        <RequestView state={node} what="the node's scorecards">
          {(got) => <NodeBody nodeId={got.nodeId} scorecards={got.scorecards} />}
        </RequestView>
      )}
      <Link to="../nodes">← Node lookup</Link>
    </Stack>
  );
}

function NodeBody({ nodeId, scorecards }: { nodeId: string; scorecards: Scorecard[] }) {
  return (
    <>
      {scorecards.map((s) => (
        <Card key={s.scorecardId} title={`The ${s.window} window`}>
          <Stack>
            <Facts
              items={[
                { label: "Scorecard", value: s.scorecardId },
                { label: "Node", value: nodeId },
                { label: "Window", value: s.window },
                { label: "State", value: s.state },
                { label: "Grade", value: <GradePill grade={s.grade} /> },
                { label: "Computed at", value: utcLabel(s.computedAt) },
              ]}
            />
            <MetricsTable metrics={s.metrics} />
          </Stack>
        </Card>
      ))}
      {scorecards.length === 1 ? (
        <div style={{ color: "var(--wh-color-text-muted)", fontSize: "var(--wh-font-size-sm)" }}>
          Only one window exists for this node so far.
        </div>
      ) : null}
    </>
  );
}
