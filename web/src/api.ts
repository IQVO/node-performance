import { PERFORMANCE_API_BASE } from "./config";
import type { ListScorecardsQuery, NodeScorecards, ScorecardList } from "./types";

/**
 * RFC 9457 problem+json body every error response from node-performance
 * returns (`type` is the bare slug: not-found, validation-failed).
 */
export interface ProblemDetails {
  type?: string;
  title?: string;
  status?: number;
  detail?: string;
  instance?: string;
}

/**
 * Every failed call -- an HTTP error with a problem+json body, an HTTP error
 * without one, or a network failure -- surfaces as one ApiError, so a screen
 * has a single shape to render: `title` (the problem's category) and `detail`
 * (what exactly was wrong), plus `slug` to branch on a specific problem
 * (e.g. `not-found`).
 */
export class ApiError extends Error {
  readonly status: number;
  readonly problem: ProblemDetails | null;
  readonly title: string;
  readonly detail: string;
  /** The problem's type slug (e.g. "not-found"); "" when unknown. */
  readonly slug: string;

  constructor(status: number, problem: ProblemDetails | null, fallbackTitle: string) {
    const title = problem?.title || fallbackTitle;
    const detail = problem?.detail ?? "";
    super(detail || title);
    this.name = "ApiError";
    this.status = status;
    this.problem = problem;
    this.title = title;
    this.detail = detail;
    this.slug = problem?.type ? (problem.type.split("/").pop() ?? "") : "";
  }
}

/** Normalizes anything thrown by a request into an ApiError. */
export function toApiError(err: unknown): ApiError {
  if (err instanceof ApiError) return err;
  const message = err instanceof Error ? err.message : String(err);
  return new ApiError(0, { detail: message }, "Network error");
}

type Query = Record<string, string | number | boolean | undefined>;

export function withQuery(path: string, query?: Query): string {
  if (!query) return path;
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && value !== "") params.set(key, String(value));
  }
  const qs = params.toString();
  return qs ? `${path}?${qs}` : path;
}

/**
 * The one door to the service. Only this context's own API is ever called
 * (`PERFORMANCE_API_BASE` = <apiOrigin>/api/node-performance): no sibling
 * context, no other service. This remote is read-only, so every request is
 * a header-less GET -- a simple CORS request.
 */
async function request<T>(path: string): Promise<T> {
  let res: Response;
  try {
    res = await fetch(`${PERFORMANCE_API_BASE}${path}`);
  } catch (err) {
    throw toApiError(err);
  }
  if (!res.ok) {
    let problem: ProblemDetails | null = null;
    try {
      problem = (await res.json()) as ProblemDetails;
    } catch {
      // non-JSON error body -- fall through with problem = null
    }
    throw new ApiError(res.status, problem, `${res.status} ${res.statusText}`.trim());
  }
  return (await res.json()) as T;
}

const seg = encodeURIComponent;

/** GET /scorecards/{nodeId} (404 not-found: nothing was observed for the node yet). */
export async function getScorecard(nodeId: string): Promise<NodeScorecards> {
  return request<NodeScorecards>(`/scorecards/${seg(nodeId)}`);
}

/** GET /scorecards?grade=&window=&limit=&after= -- one page of a grade. */
export async function listScorecards(q: ListScorecardsQuery): Promise<ScorecardList> {
  const res = await request<ScorecardList>(
    withQuery("/scorecards", { grade: q.grade, window: q.window, limit: q.limit, after: q.after }),
  );
  return { items: res.items ?? [], nextCursor: res.nextCursor || undefined };
}
