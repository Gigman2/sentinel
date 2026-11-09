import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "../App";
import { INCIDENT_ID, apiError, mockFetch, referenceIncident, referenceTimeline } from "../test-fixtures";
import type { Incident } from "../types";

const projects = { items: [{ id: "p-1", slug: "uppsel-production", name: "Uppsel Production", created_at: "2026-11-08T00:00:00Z" }] };
const detailPath = `/v1/incidents/${INCIDENT_ID}`;

function pending(): Incident {
  const inc = referenceIncident();
  return { ...inc, analysis: { status: "pending", last_error: null, latest_revision: null } };
}

beforeEach(() => {
  sessionStorage.setItem("sentinel.token", "sk_usr_test");
  window.location.hash = `#/incidents/${INCIDENT_ID}`;
});
afterEach(() => {
  vi.useRealTimers();
});

describe("incident detail", () => {
  it("shows the incident number and who recorded the cause", async () => {
    mockFetch({
      "GET /v1/projects": () => projects,
      "GET /v1/projects/p-1/incidents": () => ({ items: [], next_cursor: null, open_count: 0 }),
      [`GET ${detailPath}`]: () =>
        referenceIncident({
          status: "resolved",
          resolved_at: "2026-11-09T14:40:00Z",
          resolution: { source: "manual", note: "Connection pool exhausted after 1.8.4" },
          resolved_by: { id: "u-1", display_name: "Ama Owusu" },
        }),
      [`GET ${detailPath}/timeline`]: () => referenceTimeline(),
    });
    render(<App />);
    expect(await screen.findByText("INC-42")).toBeInTheDocument();
    expect(screen.getByText(/Resolved by Ama Owusu/)).toBeInTheDocument();
    expect(screen.getByText("Connection pool exhausted after 1.8.4")).toBeInTheDocument();
  });

  it("shows properties, findings and the timeline in order", async () => {
    mockFetch({
      "GET /v1/projects": () => projects,
      [`GET ${detailPath}`]: () => referenceIncident(),
      [`GET ${detailPath}/timeline`]: () => referenceTimeline(),
    });
    render(<App />);
    expect(await screen.findByRole("heading", { level: 1, name: "Payment errors affecting checkout" })).toBeInTheDocument();
    const props = screen.getByRole("region", { name: "Properties" });
    expect(within(props).getByText("Investigated window")).toBeInTheDocument();
    expect(within(props).getByText("13:39 – 14:24 UTC")).toBeInTheDocument();
    expect(within(props).getByText("payment-service")).toBeInTheDocument();

    const rows = await screen.findAllByTestId(/^evt-/);
    expect(rows.map((r) => r.id)).toEqual(["evt-evt-deploy", "evt-evt-alert"]);
    expect(within(rows[0]).getByText(/received later/)).toBeInTheDocument();
    // An unsafe source URL is not rendered as a link.
    expect(within(rows[1]).queryByRole("link")).toBeNull();
    expect(within(rows[0]).getByRole("link")).toHaveAttribute("rel", "noopener noreferrer");
  });

  it("polls every 3 s while pending and stops once analysis succeeds", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let calls = 0;
    const fetch = mockFetch({
      "GET /v1/projects": () => projects,
      [`GET ${detailPath}`]: () => (++calls < 3 ? pending() : referenceIncident()),
      [`GET ${detailPath}/timeline`]: () => referenceTimeline(),
    });
    render(<App />);
    expect(await screen.findByText("Analysis running")).toBeInTheDocument();
    await act(() => vi.advanceTimersByTimeAsync(3000));
    await act(() => vi.advanceTimersByTimeAsync(3000));
    expect(await screen.findByRole("region", { name: "Observed" })).toBeInTheDocument();
    const detailCalls = () => fetch.mock.calls.filter(([u]) => new URL(String(u), "http://x").pathname === detailPath).length;
    expect(detailCalls()).toBe(3);
    await act(() => vi.advanceTimersByTimeAsync(9000));
    expect(detailCalls()).toBe(3);
  });

  it("failed analysis offers a retry", async () => {
    let failed = true;
    const fetch = mockFetch({
      "GET /v1/projects": () => projects,
      [`GET ${detailPath}`]: () =>
        failed ? { ...referenceIncident(), analysis: { status: "failed", last_error: "TimeoutError: worker timed out", latest_revision: null } } : pending(),
      [`GET ${detailPath}/timeline`]: () => referenceTimeline(),
      [`POST ${detailPath}/reanalyze`]: () => {
        failed = false;
        return new Response(JSON.stringify({ job_id: "j-2" }), { status: 202 });
      },
    });
    render(<App />);
    expect(await screen.findByText("TimeoutError: worker timed out")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Retry analysis" }));
    expect(await screen.findByText("Analysis running")).toBeInTheDocument();
    expect(fetch.mock.calls.some(([u, init]) => String(u).endsWith("/reanalyze") && init?.method === "POST")).toBe(true);
  });

  it("resolve needs a note, then posts it", async () => {
    let body: unknown;
    let resolved = false;
    mockFetch({
      "GET /v1/projects": () => projects,
      [`GET ${detailPath}`]: () =>
        resolved
          ? referenceIncident({ status: "resolved", resolved_at: "2026-11-09T14:38:00Z", resolution: { source: "manual", note: "Gateway timeout lowered in 1.8.4" } })
          : referenceIncident(),
      [`GET ${detailPath}/timeline`]: () => referenceTimeline(),
      [`POST ${detailPath}/resolve`]: (_u, init) => {
        body = JSON.parse(String(init.body));
        resolved = true;
        return referenceIncident({ status: "resolved" });
      },
    });
    render(<App />);
    await userEvent.click(await screen.findByRole("button", { name: "Resolve incident" }));
    const dialog = screen.getByRole("dialog");
    const confirm = within(dialog).getByRole("button", { name: "Resolve incident" });
    expect(confirm).toBeDisabled();
    await userEvent.type(within(dialog).getByLabelText(/Final cause/), "Gateway timeout lowered in 1.8.4");
    await userEvent.click(confirm);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(body).toEqual({ note: "Gateway timeout lowered in 1.8.4" });
    expect(await screen.findByText(/Cause · recorded at resolution/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Resolve incident" })).toBeNull();
  });

  it("401 while polling shows sign-in and stops polling", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let calls = 0;
    const fetch = mockFetch({
      "GET /v1/projects": () => projects,
      [`GET ${detailPath}`]: () => (++calls === 1 ? pending() : apiError(401, "unauthorized")),
      [`GET ${detailPath}/timeline`]: () => referenceTimeline(),
    });
    render(<App />);
    await screen.findByText("Analysis running");
    await act(() => vi.advanceTimersByTimeAsync(3000));
    expect(await screen.findByRole("heading", { name: "Sign in to Sentinel" })).toBeInTheDocument();
    const before = fetch.mock.calls.length;
    await act(() => vi.advanceTimersByTimeAsync(9000));
    expect(fetch.mock.calls.length).toBe(before);
  });

  it("ignores a late response after navigation", async () => {
    let release!: () => void;
    const otherId = "6f1c2a90-9999-4c4c-8888-000000000009";
    mockFetch({
      "GET /v1/projects": () => projects,
      [`GET ${detailPath}`]: () => new Promise((resolve) => { release = () => resolve(referenceIncident()); }),
      [`GET ${detailPath}/timeline`]: () => referenceTimeline(),
      [`GET /v1/incidents/${otherId}`]: () => referenceIncident({ id: otherId, title: "Other incident" }),
      [`GET /v1/incidents/${otherId}/timeline`]: () => ({ items: [], next_cursor: null }),
    });
    render(<App />);
    await waitFor(() => expect(release).toBeDefined());
    act(() => { window.location.hash = `#/incidents/${otherId}`; window.dispatchEvent(new HashChangeEvent("hashchange")); });
    expect(await screen.findByRole("heading", { level: 1, name: "Other incident" })).toBeInTheDocument();
    await act(async () => release());
    expect(screen.getByRole("heading", { level: 1, name: "Other incident" })).toBeInTheDocument();
    expect(screen.queryByText("Payment errors affecting checkout")).toBeNull();
  });

  it("a citation scrolls to and highlights its timeline row", async () => {
    mockFetch({
      "GET /v1/projects": () => projects,
      [`GET ${detailPath}`]: () => referenceIncident(),
      [`GET ${detailPath}/timeline`]: () => referenceTimeline(),
    });
    const scroll = vi.fn();
    Element.prototype.scrollIntoView = scroll;
    render(<App />);
    await screen.findAllByTestId(/^evt-/);
    await userEvent.click(screen.getAllByRole("button", { name: /Deployment · 14:02/ })[0]);
    expect(scroll).toHaveBeenCalled();
    expect(document.getElementById("evt-evt-deploy")).toHaveAttribute("data-highlight", "true");
  });
});
