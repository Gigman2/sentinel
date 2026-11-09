// Incident list filters, kept in the hash query (#/?status=open&severity=…)
// so a filtered view can be shared and survives a reload.
import type { Severity } from "./types";

export type StatusFilter = "all" | "open" | "resolved";
export type RangeFilter = "" | "24h" | "7d" | "30d";

export interface ListFilters {
  status: StatusFilter;
  severity: Severity | "";
  /** Component slug; "" is every component. */
  component: string;
  range: RangeFilter;
}

export const NO_FILTERS: ListFilters = { status: "all", severity: "", component: "", range: "" };

const STATUSES: StatusFilter[] = ["all", "open", "resolved"];
const SEVERITIES: Severity[] = ["critical", "error", "warning", "info"];
const RANGES: RangeFilter[] = ["24h", "7d", "30d"];
const RANGE_MS: Record<Exclude<RangeFilter, "">, number> = { "24h": 86_400_000, "7d": 7 * 86_400_000, "30d": 30 * 86_400_000 };

/** Reads filters from a query string. Unknown values are ignored. */
export function parseFilters(query: string): ListFilters {
  const q = new URLSearchParams(query);
  const pick = <T extends string>(v: string | null, allowed: readonly T[], def: T): T => (v !== null && (allowed as readonly string[]).includes(v) ? (v as T) : def);
  return {
    status: pick(q.get("status"), STATUSES, "all"),
    severity: pick(q.get("severity"), SEVERITIES, ""),
    component: (q.get("component") ?? "").slice(0, 200),
    range: pick(q.get("range"), RANGES, ""),
  };
}

/** The list route for these filters: "#/" when none are set. */
export function filtersHash(f: ListFilters): string {
  const q = new URLSearchParams();
  if (f.status !== "all") q.set("status", f.status);
  if (f.severity) q.set("severity", f.severity);
  if (f.component) q.set("component", f.component);
  if (f.range) q.set("range", f.range);
  const s = q.toString();
  return s ? `#/?${s}` : "#/";
}

/** The `since` time for a range, as RFC 3339, or null for any time. */
export function sinceFor(range: RangeFilter, now: Date): string | null {
  return range ? new Date(now.getTime() - RANGE_MS[range]).toISOString() : null;
}

export function anyFilter(f: ListFilters): boolean {
  return f.status !== "all" || f.severity !== "" || f.component !== "" || f.range !== "";
}
