/** Wire types of apis/openapi.yaml (camelCase, as the REST contract names them). */

/** The grades a scorecard can carry; PROVISIONAL is below the observation minimum (50). */
export const GRADES = ["A", "B", "C", "D", "PROVISIONAL"] as const;
export type Grade = (typeof GRADES)[number];

/** The rolling windows every node is graded over. */
export const WINDOWS = ["7d", "30d"] as const;
export type Window = (typeof WINDOWS)[number];

/** PROVISIONAL (below the observation minimum) or GRADED. */
export type ScorecardState = "PROVISIONAL" | "GRADED";

/** The metrics of one window (the OpenAPI Metrics schema, ADR 0002 A5-A10). */
export interface Metrics {
  onTimeHandoverRate: number;
  lateAckRate: number;
  rejectRate: number;
  expiryRate: number;
  promiseKeptRate: number;
  feedFreshnessP95Minutes: number;
  observations: number;
}

/** One scorecard of one window (the OpenAPI Scorecard schema). */
export interface Scorecard {
  scorecardId: string;
  nodeId: string;
  window: Window;
  state: ScorecardState;
  grade: Grade;
  metrics: Metrics;
  /** When the last recompute ran; omitted before the first one. */
  computedAt?: string;
}

/** The answer of GET /scorecards/{nodeId}: both windows of one node, 7d first. */
export interface NodeScorecards {
  nodeId: string;
  scorecards: Scorecard[];
}

/** One page of GET /scorecards: ordered by scorecardId ascending. */
export interface ScorecardList {
  items: Scorecard[];
  nextCursor?: string;
}

/** The query of GET /scorecards: grade is required, the rest is optional. */
export interface ListScorecardsQuery {
  grade: Grade;
  window?: Window;
  limit?: number;
  cursor?: string;
}
