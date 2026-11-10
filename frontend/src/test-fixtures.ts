// Test helpers: the reference incident and a fetch mock that routes by
// method and path.
import { vi } from "vitest";
import type { Incident, IncidentListItem, Page, TimelineItem } from "./types";

export const INCIDENT_ID = "6f1c2a90-1111-4c4c-8888-000000000001";

export function referenceIncident(over: Partial<Incident> = {}): Incident {
  return {
    id: INCIDENT_ID,
    number: 42,
    project_id: "p-1",
    component: { id: "c-1", slug: "payment-service", display_name: "Payment Service" },
    environment: "production",
    fingerprint: "payments-errors",
    status: "open",
    severity: "critical",
    title: "Payment errors affecting checkout",
    first_seen_at: "2026-11-09T14:09:12Z",
    last_seen_at: "2026-11-09T14:09:12Z",
    evidence_window: { start: "2026-11-09T13:39:12Z", end: "2026-11-09T14:24:12Z" },
    resolved_at: null,
    resolution: null,
    resolved_by: null,
    trigger_event_id: "evt-alert",
    signal_count: 1,
    analysis: {
      status: "succeeded",
      last_error: null,
      latest_revision: {
        id: "rev-1",
        revision_number: 1,
        created_at: "2026-11-09T14:09:48Z",
        findings: [
          { kind: "observation", rule_id: "trigger_alert", text: "payment-service alert 'Payment errors affecting checkout' first fired at 14:09 UTC.", limitations: null, evidence_ids: ["evt-alert"] },
          { kind: "observation", rule_id: "deployment_in_window", text: "payment-service version 1.8.4 was deployed at 14:02 UTC.", limitations: null, evidence_ids: ["evt-deploy"] },
          { kind: "hypothesis", rule_id: "deployment_near_onset", text: "The payment-service deployment of version 1.8.4 at 14:02 UTC may be related to the alert at 14:09 UTC.", limitations: "Timing alone cannot establish causation.", evidence_ids: ["evt-deploy", "evt-alert"] },
          { kind: "unknown", rule_id: "no_error_evidence", text: "No matching error events were found for payment-service in the investigated window.", limitations: null, evidence_ids: [] },
          { kind: "next_check", rule_id: "inspect_telemetry", text: "Inspect payment-service logs and metrics from 13:39 UTC to 14:24 UTC.", limitations: null, evidence_ids: [] },
        ],
      },
    },
    ...over,
  };
}

export function referenceTimeline(): Page<TimelineItem> {
  return {
    items: [
      { event_id: "evt-deploy", type: "deployment.completed", occurred_at: "2026-11-09T14:02:31Z", received_at: "2026-11-09T14:11:03Z", summary: "Deploy payment-service 1.8.4", severity: null, source_url: "https://deploys.example/payments/1.8.4", link_reason: "same_component_time_window", attributes: {}, details: { version: "1.8.4" } },
      { event_id: "evt-alert", type: "alert.triggered", occurred_at: "2026-11-09T14:09:12Z", received_at: "2026-11-09T14:09:12Z", summary: "Payment errors affecting checkout", severity: "critical", source_url: "javascript:alert(1)", link_reason: "trigger", attributes: {}, details: {} },
    ],
    next_cursor: null,
  };
}

export function listItem(over: Partial<IncidentListItem> = {}): IncidentListItem {
  return {
    id: INCIDENT_ID,
    status: "open",
    severity: "critical",
    title: "Payment errors affecting checkout",
    component: { slug: "payment-service", display_name: "Payment Service" },
    environment: "production",
    first_seen_at: "2026-11-09T14:09:12Z",
    last_seen_at: "2026-11-09T14:09:12Z",
    number: 42,
    analysis_status: "succeeded",
    signal_count: 1,
    resolution_note: null,
    ...over,
  };
}

type Handler = (url: URL, init: RequestInit) => unknown | Response | Promise<unknown | Response>;

/** Installs a fetch mock. Routes are "METHOD /path" → handler; the handler
 * returns a JSON body (200) or a Response. Unmatched requests return 404. */
export function mockFetch(routes: Record<string, Handler>) {
  const fn = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const url = new URL(String(input), "http://localhost");
    const key = `${(init.method ?? "GET").toUpperCase()} ${url.pathname}`;
    const handler = routes[key];
    if (!handler) return json({ error: { code: "not_found", message: `no mock for ${key}` }, request_id: "req-test" }, 404);
    const out = await handler(url, init);
    return out instanceof Response ? out : json(out, 200);
  });
  vi.stubGlobal("fetch", fn);
  return fn;
}

export function json(body: unknown, status = 200): Response {
  return new Response(status === 204 ? null : JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

export function apiError(status: number, code: string, message = code): Response {
  return json({ error: { code, message }, request_id: "req-test" }, status);
}
