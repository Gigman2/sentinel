import { expect, it } from "vitest";
import { parseRoute } from "./App";
import { filtersHash, NO_FILTERS, parseFilters, sinceFor } from "./filters";

it("parses list filters and ignores unknown values", () => {
  expect(parseFilters("status=resolved&severity=warning&component=checkout-api&range=30d")).toEqual({
    status: "resolved",
    severity: "warning",
    component: "checkout-api",
    range: "30d",
  });
  expect(parseFilters("status=closed&severity=Critical&range=1y")).toEqual(NO_FILTERS);
});

it("round-trips filters through the hash", () => {
  const f = { status: "open", severity: "critical", component: "a b&c", range: "24h" } as const;
  expect(filtersHash(NO_FILTERS)).toBe("#/");
  expect(parseRoute(filtersHash(f))).toEqual({ name: "list", filters: f });
});

it("routes every page", () => {
  expect(parseRoute("#/alerts")).toEqual({ name: "alerts" });
  expect(parseRoute("#/integrations")).toEqual({ name: "integrations" });
  expect(parseRoute("#/incidents/abc")).toEqual({ name: "detail", id: "abc" });
  expect(parseRoute("")).toEqual({ name: "list", filters: NO_FILTERS });
});

it("computes since from a range", () => {
  const now = new Date("2026-11-09T14:00:00Z");
  expect(sinceFor("", now)).toBeNull();
  expect(sinceFor("24h", now)).toBe("2026-11-08T14:00:00.000Z");
  expect(sinceFor("7d", now)).toBe("2026-11-02T14:00:00.000Z");
});
