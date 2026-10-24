import { NO_VALUE } from "@warehouse/ui-kit";

/** "2026-10-22 14:03:00Z" for an RFC 3339 instant; the raw text when unparseable. */
export function utcLabel(iso: string | undefined): string {
  if (!iso) return NO_VALUE;
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const d = new Date(t).toISOString();
  return `${d.slice(0, 10)} ${d.slice(11, 19)}Z`;
}

/** A rate as a percentage with one decimal: 0.984 -> "98.4%". */
export function pct(rate: number): string {
  return `${(rate * 100).toFixed(1)}%`;
}

/** Whole minutes: 38 -> "38 min". */
export function minutes(m: number): string {
  return `${m} min`;
}

/** The node id rule of the contract (the OpenAPI NodeId parameter): 1..64
 *  characters, and the domain refuses whitespace, control characters and
 *  "/" (a scorecardId is nodeId + ':' + window). */
export function nodeIdProblem(id: string): string | null {
  if (id.length === 0) return "Enter a node id.";
  if (id.length > 64) return "A node id is at most 64 characters.";
  // eslint-disable-next-line no-control-regex
  if (/[\s/\u0000-\u001f\u007f]/.test(id)) return "A node id cannot contain spaces, control characters or '/'.";
  return null;
}

/** A finite number typed into a field, or null when the text is empty or not a number. */
export function finiteNumber(raw: string): number | null {
  const s = raw.trim();
  if (s === "") return null;
  const n = Number(s);
  return Number.isFinite(n) ? n : null;
}
