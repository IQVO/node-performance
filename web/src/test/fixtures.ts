import type { Metrics, NodeScorecards, Scorecard, ScorecardList } from "../types";

export const METRICS_A: Metrics = {
  onTimeHandoverRate: 0.93,
  lateAckRate: 0.04,
  rejectRate: 0.02,
  expiryRate: 0.01,
  promiseKeptRate: 0.92,
  feedFreshnessP95Minutes: 55,
  observations: 412,
};

export const METRICS_B: Metrics = {
  onTimeHandoverRate: 0.962,
  lateAckRate: 0.021,
  rejectRate: 0.012,
  expiryRate: 0.004,
  promiseKeptRate: 0.951,
  feedFreshnessP95Minutes: 38,
  observations: 1874,
};

export const METRICS_PROVISIONAL: Metrics = {
  onTimeHandoverRate: 1,
  lateAckRate: 0,
  rejectRate: 0,
  expiryRate: 0,
  promiseKeptRate: 1,
  feedFreshnessP95Minutes: 0,
  observations: 12,
};

export function scorecard(over: Partial<Scorecard> = {}): Scorecard {
  return {
    scorecardId: "N-0042:7d",
    nodeId: "N-0042",
    window: "7d",
    state: "GRADED",
    grade: "C",
    metrics: METRICS_A,
    computedAt: "2026-10-22T14:03:00Z",
    ...over,
  };
}

/** N-0042: the OpenAPI example answer of GET /scorecards/{nodeId}. */
export function nodeScorecards(over: Partial<NodeScorecards> = {}): NodeScorecards {
  return {
    nodeId: "N-0042",
    scorecards: [
      scorecard(),
      scorecard({
        scorecardId: "N-0042:30d",
        window: "30d",
        grade: "B",
        metrics: METRICS_B,
      }),
    ],
    ...over,
  };
}

/** One page of grade-A scorecards, three nodes across two windows. */
export function gradeAPage(over: Partial<ScorecardList> = {}): ScorecardList {
  return {
    items: [
      scorecard({ scorecardId: "N-0042:7d", grade: "A" }),
      scorecard({ scorecardId: "N-0042:30d", window: "30d", grade: "A", metrics: METRICS_B }),
      scorecard({ nodeId: "N-0051", scorecardId: "N-0051:7d", grade: "A" }),
    ],
    nextCursor: "N-0051:7d",
    ...over,
  };
}
