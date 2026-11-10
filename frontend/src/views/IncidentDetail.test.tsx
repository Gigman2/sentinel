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
  return { ...inc, analysis: { status: "pending", last_error: null, started_at: null, finished_at: null, revision_count: 0, step: null, step_started_at: null, latest_revision: null } };
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

  describe("revision history", () => {
    // Revision 1 analysed the alert alone; the deployment arrived late and
    // revision 2 (the reference findings) replaced it.
    const rev1 = {
      id: "rev-1",
      revision_number: 1,
      created_at: "2026-11-09T14:09:48Z",
      findings: [
        { id: "r1-1", kind: "observation", rule_id: "trigger_alert", text: "payment-service alert first fired at 14:09 UTC.", limitations: null, evidence_ids: ["evt-alert"], feedback: { useful: 0, incorrect: 0, mine: null }, check: null },
        { id: "r1-2", kind: "unknown", rule_id: "no_deployment_evidence", text: "No deployment information is available for payment-service in the investigated window.", limitations: null, evidence_ids: [], feedback: { useful: 0, incorrect: 0, mine: null }, check: null },
      ],
    };
    const history = {
      items: [
        {
          revision_number: 2,
          created_at: "2026-11-09T14:11:20Z",
          reason: "late_event",
          finding_count: 5,
          changes: {
            added: [
              { kind: "observation", rule_id: "deployment_in_window" },
              { kind: "hypothesis", rule_id: "deployment_near_onset" },
              { kind: "next_check", rule_id: "compare_by_version" },
              { kind: "next_check", rule_id: "review_change" },
            ],
            removed: [{ kind: "unknown", rule_id: "no_deployment_evidence" }],
          },
          new_evidence: [{ event_id: "evt-deploy", type: "deployment.completed", received_at: "2026-11-09T14:11:03Z" }],
        },
        { revision_number: 1, created_at: "2026-11-09T14:09:48Z", reason: "incident_created", finding_count: 2, changes: null, new_evidence: [{ event_id: "evt-alert", type: "alert.triggered", received_at: "2026-11-09T14:09:12Z" }] },
      ],
    };
    function twoRevisions(): Incident {
      const inc = referenceIncident();
      return {
        ...inc,
        analysis: {
          ...inc.analysis,
          started_at: "2026-11-09T14:11:05Z",
          finished_at: "2026-11-09T14:11:20Z",
          revision_count: 2,
          latest_revision: { ...inc.analysis.latest_revision!, id: "rev-2", revision_number: 2, created_at: "2026-11-09T14:11:20Z" },
        },
      };
    }

    it("lists revisions with their reason and changes, and views an old one", async () => {
      const fetch = mockFetch({
        "GET /v1/projects": () => projects,
        [`GET ${detailPath}`]: () => twoRevisions(),
        [`GET ${detailPath}/timeline`]: () => referenceTimeline(),
        [`GET ${detailPath}/revisions`]: () => history,
        [`GET ${detailPath}/revisions/1`]: () => rev1,
      });
      render(<App />);
      const analysis = await screen.findByRole("region", { name: "Analysis" });
      expect(within(analysis).getByText("Started").nextSibling).toHaveTextContent("14:11:05 UTC");
      expect(within(analysis).getByText("Completed").nextSibling).toHaveTextContent("14:11:20 UTC");
      expect(within(analysis).getByText("Elapsed").nextSibling).toHaveTextContent("0:15");

      await userEvent.click(within(analysis).getByRole("button", { name: "History" }));
      const list = await within(analysis).findByRole("list", { name: "Revision history" });
      const items = within(list).getAllByRole("listitem");
      expect(items).toHaveLength(2);
      expect(items[0]).toHaveTextContent("Revision 2");
      expect(items[0]).toHaveTextContent("Late evidence: deployment received at 14:11:03 UTC.");
      expect(items[0]).toHaveTextContent("+ Observed, + Hypothesis, + Check ×2, − Gap");
      expect(items[1]).toHaveTextContent("Initial analysis when the incident opened.");
      expect(items[1]).toHaveTextContent("Initial analysis · 2 findings");
      expect(within(items[0]).getByRole("button", { name: "Viewing revision 2" })).toHaveAttribute("aria-pressed", "true");

      await userEvent.click(within(items[1]).getByRole("button", { name: "View revision 1" }));
      // While revision 1 loads, the summary says so (never "no analysis has run").
      expect(screen.queryByText(/No analysis has run/)).toBeNull();
      const banner = await screen.findByRole("status", { name: "" });
      await waitFor(() => expect(screen.getByText(/No deployment information is available/)).toBeInTheDocument());
      expect(banner).toHaveTextContent("Viewing Revision 1, superseded at 14:11:20 UTC.");
      expect(screen.queryByText(/may be related to the alert/)).toBeNull();
      expect(screen.getByText(/Analysis revision 1 from/)).toBeInTheDocument();
      expect(within(items[1]).getByRole("button", { name: "Viewing revision 1" })).toBeInTheDocument();
      expect(fetch.mock.calls.some(([u]) => String(u).endsWith("/revisions/1"))).toBe(true);

      await userEvent.click(screen.getByRole("button", { name: "Back to Revision 2" }));
      expect(screen.queryByText(/Viewing Revision 1/)).toBeNull();
      expect(screen.getAllByText(/may be related to the alert/).length).toBeGreaterThan(0);
    });

    it("says an older revision is loading, then that it couldn't load", async () => {
      let fail!: () => void;
      mockFetch({
        "GET /v1/projects": () => projects,
        [`GET ${detailPath}`]: () => twoRevisions(),
        [`GET ${detailPath}/timeline`]: () => referenceTimeline(),
        [`GET ${detailPath}/revisions`]: () => history,
        [`GET ${detailPath}/revisions/1`]: () => new Promise((resolve) => { fail = () => resolve(apiError(503, "database_unavailable")); }),
      });
      render(<App />);
      const analysis = await screen.findByRole("region", { name: "Analysis" });
      await userEvent.click(within(analysis).getByRole("button", { name: "History" }));
      await userEvent.click(await within(analysis).findByRole("button", { name: "View revision 1" }));
      const summary = screen.getByRole("region", { name: "Summary" });
      expect(summary).toHaveTextContent("Loading revision 1…");
      expect(summary).not.toHaveTextContent("No analysis has run");
      await act(async () => fail());
      await waitFor(() => expect(summary).toHaveTextContent("Revision 1 couldn't load."));
    });

    it("hides History until there is a revision", async () => {
      mockFetch({
        "GET /v1/projects": () => projects,
        [`GET ${detailPath}`]: () => pending(),
        [`GET ${detailPath}/timeline`]: () => referenceTimeline(),
      });
      render(<App />);
      const analysis = await screen.findByRole("region", { name: "Analysis" });
      expect(within(analysis).queryByRole("button", { name: "History" })).toBeNull();
      expect(within(analysis).queryByText("Started")).toBeNull();
    });

    it("counts elapsed time live while the job runs", async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      vi.setSystemTime(new Date("2026-11-09T14:09:55Z"));
      const running = pending();
      running.analysis = { ...running.analysis, status: "running", started_at: "2026-11-09T14:09:14Z" };
      mockFetch({
        "GET /v1/projects": () => projects,
        [`GET ${detailPath}`]: () => running,
        [`GET ${detailPath}/timeline`]: () => referenceTimeline(),
      });
      render(<App />);
      const analysis = await screen.findByRole("region", { name: "Analysis" });
      await waitFor(() => expect(within(analysis).getByText("Elapsed").nextSibling).toHaveTextContent(/^0:4\d$/));
      const before = within(analysis).getByText("Elapsed").nextSibling!.textContent!;
      await act(() => vi.advanceTimersByTimeAsync(2000));
      expect(within(analysis).getByText("Elapsed").nextSibling!.textContent).not.toBe(before);
      expect(within(analysis).queryByText("Completed")).toBeNull();
    });

    it("shows a failed job's time as Failed", async () => {
      const inc = referenceIncident();
      inc.analysis = { ...inc.analysis, status: "failed", last_error: "RuntimeError: boom", started_at: "2026-11-09T14:09:14Z", finished_at: "2026-11-09T14:10:15Z" };
      mockFetch({
        "GET /v1/projects": () => projects,
        [`GET ${detailPath}`]: () => inc,
        [`GET ${detailPath}/timeline`]: () => referenceTimeline(),
      });
      render(<App />);
      const analysis = await screen.findByRole("region", { name: "Analysis" });
      expect(within(analysis).getByText("Failed", { selector: ".prop > span:first-child" }).nextSibling).toHaveTextContent("14:10:15 UTC");
      expect(within(analysis).getByText("Elapsed").nextSibling).toHaveTextContent("1:01");
      // The findings on screen are from the last revision, written earlier.
      expect(within(analysis).getByText("Revised").nextSibling).toHaveTextContent("14:09:48 UTC");
    });

    it("does not count elapsed time while a job waits to retry", async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      vi.setSystemTime(new Date("2026-11-09T14:12:00Z"));
      const inc = referenceIncident();
      inc.analysis = { ...inc.analysis, status: "pending", last_error: "RuntimeError: boom", started_at: "2026-11-09T14:10:00Z", finished_at: null };
      mockFetch({
        "GET /v1/projects": () => projects,
        [`GET ${detailPath}`]: () => inc,
        [`GET ${detailPath}/timeline`]: () => referenceTimeline(),
      });
      render(<App />);
      const analysis = await screen.findByRole("region", { name: "Analysis" });
      expect(within(analysis).getByText("Waiting to retry")).toBeInTheDocument();
      expect(within(analysis).queryByText("Elapsed")).toBeNull();
      expect(within(analysis).getByText("Revised").nextSibling).toHaveTextContent("14:09:48 UTC");
    });

    it("hides Revised when the newest job wrote the revision on screen", async () => {
      mockFetch({
        "GET /v1/projects": () => projects,
        [`GET ${detailPath}`]: () => referenceIncident(),
        [`GET ${detailPath}/timeline`]: () => referenceTimeline(),
      });
      render(<App />);
      const analysis = await screen.findByRole("region", { name: "Analysis" });
      expect(within(analysis).getByText("Completed")).toBeInTheDocument();
      expect(within(analysis).queryByText("Revised")).toBeNull();
    });
  });

  it("shows which evidence sources returned data", async () => {
    const inc = referenceIncident();
    inc.evidence_sources = inc.evidence_sources.map((s) => (s.name === "deploys" ? { ...s, event_count: 0, status: "no_matching_data" } : s));
    mockFetch({
      "GET /v1/projects": () => projects,
      [`GET ${detailPath}`]: () => inc,
      [`GET ${detailPath}/timeline`]: () => referenceTimeline(),
    });
    render(<App />);
    const sources = await screen.findByRole("region", { name: "Evidence sources" });
    expect(within(sources).getByText("1 of 4 returned data")).toBeInTheDocument();
    const rows = within(sources).getAllByRole("listitem");
    expect(rows.map((r) => r.textContent)).toEqual([
      "deploys0 eventsNo matching data found",
      "monitor1 eventRetrieved",
      "Logs—Not connected",
      "Metrics—Not connected",
    ]);
  });

  describe("summary and steps", () => {
    function render_(inc: Incident) {
      mockFetch({
        "GET /v1/projects": () => projects,
        [`GET ${detailPath}`]: () => inc,
        [`GET ${detailPath}/timeline`]: () => referenceTimeline(),
      });
      render(<App />);
    }

    it("renders times in mono and the unknown cause in bold, as text", async () => {
      const inc = referenceIncident();
      inc.analysis.latest_revision!.summary = [
        { text: "Alert <b>fired</b> at ", kind: "text" },
        { text: "14:09", kind: "time" },
        { text: ". Deployment information unavailable. ", kind: "text" },
        { text: "Cause: Unknown.", kind: "strong" },
      ];
      render_(inc);
      const summary = await screen.findByRole("region", { name: "Summary" });
      expect(within(summary).getByText("Built only from the 5 findings below")).toBeInTheDocument();
      expect(within(summary).getByText("Revision 1")).toBeInTheDocument();
      expect(within(summary).getByText("14:09")).toHaveClass("mono");
      expect(within(summary).getByText("Cause: Unknown.").tagName).toBe("B");
      // Server text is never parsed as HTML.
      expect(within(summary).getByText(/Alert <b>fired<\/b> at/)).toBeInTheDocument();
      expect(summary.querySelectorAll("b")).toHaveLength(1);
    });

    it("says when a revision has no summary", async () => {
      const inc = referenceIncident();
      inc.analysis.latest_revision!.summary = [];
      render_(inc);
      const summary = await screen.findByRole("region", { name: "Summary" });
      expect(within(summary).getByText("Not available")).toBeInTheDocument();
      expect(within(summary).getByText(/No summary was written for this revision/)).toBeInTheDocument();
    });

    it("lists the steps while running", async () => {
      const inc = pending();
      inc.analysis = { ...inc.analysis, status: "running", started_at: "2026-11-09T14:09:14Z", step: "draft_findings", step_started_at: "2026-11-09T14:09:20Z" };
      render_(inc);
      const steps = await screen.findByRole("list", { name: "Analysis steps" });
      expect(within(steps).getAllByRole("listitem").map((li) => li.textContent)).toEqual([
        "✓Collect evidencedone",
        "●Draft findingsrunning",
        "○Save revisionqueued",
      ]);
      expect(within(await screen.findByRole("region", { name: "Summary" })).getByText("Not available yet")).toBeInTheDocument();
    });

    it("names the step a failed analysis stopped at", async () => {
      const inc = pending();
      inc.analysis = { ...inc.analysis, status: "failed", last_error: "RuntimeError: rule exploded", started_at: "2026-11-09T14:09:14Z", finished_at: "2026-11-09T14:10:15Z", step: "draft_findings", step_started_at: "2026-11-09T14:09:20Z" };
      render_(inc);
      expect(await screen.findByText("Analysis failed at step 2 of 3: Draft findings")).toBeInTheDocument();
      expect(screen.getByText("RuntimeError: rule exploded")).toBeInTheDocument();
      const steps = screen.getByRole("list", { name: "Analysis steps" });
      expect(within(steps).getAllByRole("listitem").map((li) => li.textContent)).toEqual([
        "✓Collect evidencedone",
        "✕Draft findingsfailed",
        "○Save revisionnot run",
      ]);
    });

    it("shows a running job with no recorded step on the first step", async () => {
      const inc = pending();
      inc.analysis = { ...inc.analysis, status: "running", started_at: "2026-11-09T14:09:14Z" };
      render_(inc);
      const steps = await screen.findByRole("list", { name: "Analysis steps" });
      expect(within(steps).getAllByRole("listitem")[0]).toHaveTextContent("Collect evidencerunning");
    });

    it("says findings were drafted when saving them failed", async () => {
      const inc = pending();
      inc.analysis = { ...inc.analysis, status: "failed", last_error: "ForeignKeyViolation: x", started_at: "2026-11-09T14:09:14Z", finished_at: "2026-11-09T14:10:15Z", step: "save_revision", step_started_at: "2026-11-09T14:09:20Z" };
      render_(inc);
      expect(await screen.findByText("Analysis failed at step 3 of 3: Save revision")).toBeInTheDocument();
      const summary = screen.getByRole("region", { name: "Summary" });
      expect(summary).toHaveTextContent("Analysis failed after drafting findings, before they were saved");
      expect(summary).not.toHaveTextContent("before findings were drafted");
    });

    it("shows a job waiting to retry at the step that failed", async () => {
      const inc = pending();
      inc.analysis = { ...inc.analysis, status: "pending", started_at: "2026-11-09T14:09:14Z", step: "draft_findings", step_started_at: "2026-11-09T14:09:20Z" };
      render_(inc);
      const steps = await screen.findByRole("list", { name: "Analysis steps" });
      expect(within(steps).getAllByRole("listitem")[1]).toHaveTextContent("Draft findingswaiting to retry");
    });
  });
});

