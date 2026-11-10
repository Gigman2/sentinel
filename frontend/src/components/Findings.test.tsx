import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { apiError, mockFetch, referenceIncident, referenceTimeline } from "../test-fixtures";
import { useState, type ComponentProps } from "react";
import { Findings } from "./Findings";
import type { SavedPatch } from "./FindingRow";

/** Findings with saved changes laid over its input, as the incident page does. */
function Harness(props: Omit<ComponentProps<typeof Findings>, "onSaved">) {
  const [saved, setSaved] = useState<Record<string, SavedPatch>>({});
  return (
    <Findings
      {...props}
      findings={props.findings.map((f) => ({ ...f, ...saved[f.id] }))}
      onSaved={(id, patch) => setSaved((s) => ({ ...s, [id]: { ...s[id], ...patch } }))}
    />
  );
}

const findings = referenceIncident().analysis.latest_revision!.findings;
const events = new Map(referenceTimeline().items.map((e) => [e.event_id, e]));

describe("Findings", () => {
  it("groups each kind under its heading, in order", () => {
    render(<Harness findings={findings} events={events} layout="grouped" onCite={() => {}} />);
    const headings = screen.getAllByRole("heading", { level: 3 }).map((h) => h.textContent);
    expect(headings).toEqual(["Observed", "Hypothesis", "Gaps", "Check next"]);
    const observed = screen.getByRole("region", { name: "Observed" });
    expect(within(observed).getAllByRole("listitem")).toHaveLength(2);
    const gaps = screen.getByRole("region", { name: "Gaps" });
    expect(within(gaps).getByText(/No matching error events/)).toBeInTheDocument();
  });

  it("tags every finding with its kind in words, not only colour", () => {
    render(<Harness findings={findings} events={events} layout="grouped" onCite={() => {}} />);
    // Heading plus one tag per finding.
    expect(within(screen.getByRole("region", { name: "Observed" })).getAllByText("Observed")).toHaveLength(3);
    expect(within(screen.getByRole("region", { name: "Hypothesis" })).getAllByText("Hypothesis")).toHaveLength(2);
    expect(within(screen.getByRole("region", { name: "Gaps" })).getByText("Gap")).toBeInTheDocument();
  });

  it("marks hypotheses as possible and shows their limitations", () => {
    render(<Harness findings={findings} events={events} layout="grouped" onCite={() => {}} />);
    const hyp = screen.getByRole("region", { name: "Hypothesis" });
    expect(within(hyp).getByText("Possible:")).toBeInTheDocument();
    expect(within(hyp).getByText("Timing alone cannot establish causation.")).toBeInTheDocument();
    expect(within(hyp).getByText("Rests on")).toBeInTheDocument();
  });

  it("hides empty groups", () => {
    render(<Harness findings={findings.filter((f) => f.kind !== "unknown")} events={events} layout="grouped" onCite={() => {}} />);
    expect(screen.queryByRole("region", { name: "Gaps" })).toBeNull();
  });

  it("stream layout has no group headings but still tags every finding", () => {
    render(<Harness findings={findings} events={events} layout="stream" onCite={() => {}} />);
    expect(screen.queryAllByRole("heading", { level: 3 })).toHaveLength(0);
    expect(screen.getAllByRole("listitem")).toHaveLength(5);
    expect(screen.getByText("Check")).toBeInTheDocument();
  });

  it("citation buttons report the cited event", async () => {
    const onCite = vi.fn();
    render(<Harness findings={findings} events={events} layout="grouped" onCite={onCite} />);
    await userEvent.click(screen.getAllByRole("button", { name: /Deployment · 14:02/ })[0]);
    expect(onCite).toHaveBeenCalledWith("evt-deploy");
  });

  it("labels a citation whose event is not loaded", () => {
    const f = [{ ...findings[0], evidence_ids: ["evt-missing"] }];
    render(<Harness findings={f} events={events} layout="grouped" onCite={() => {}} />);
    expect(screen.getByRole("button", { name: /Event/ })).toBeInTheDocument();
  });

  describe("feedback", () => {
    afterEach(() => vi.unstubAllGlobals());
    const observed = () => screen.getAllByRole("listitem")[0];
    const body = (call: unknown[]) => JSON.parse(String((call[1] as RequestInit).body));

    it("marks a finding useful, then clears it on a second click", async () => {
      let release!: () => void;
      const fetch = mockFetch({
        "PUT /v1/findings/f-1/feedback": () => new Promise((r) => { release = () => r({ useful: 2, incorrect: 0, mine: "useful" }); }),
        // Two other engineers voted since the page loaded; the estimate would say 1.
        "DELETE /v1/findings/f-1/feedback": () => ({ useful: 2, incorrect: 0, mine: null }),
      });
      render(<Harness findings={findings} events={events} layout="stream" onCite={() => {}} />);
      const useful = within(observed()).getByRole("button", { name: /^Useful/ });
      await userEvent.click(useful);
      // Optimistic: pressed with the viewer counted before the server answers.
      expect(useful).toHaveAttribute("aria-pressed", "true");
      expect(useful).toHaveTextContent("Useful1");
      expect(body(fetch.mock.calls[0])).toEqual({ verdict: "useful" });
      release();
      await waitFor(() => expect(useful).toHaveTextContent("Useful2")); // the server's count wins
      await waitFor(() => expect(useful).toBeEnabled());

      await userEvent.click(useful);
      await waitFor(() => expect(useful).toHaveAttribute("aria-pressed", "false"));
      expect(useful).toHaveTextContent("Useful2"); // the server's count, not the local estimate
      expect(fetch.mock.calls[1][1]).toMatchObject({ method: "DELETE" });
    });

    it("rolls back and says so when saving fails", async () => {
      mockFetch({ "PUT /v1/findings/f-1/feedback": () => apiError(503, "database_unavailable", "the service is temporarily unavailable") });
      render(<Harness findings={findings} events={events} layout="stream" onCite={() => {}} />);
      const incorrect = within(observed()).getByRole("button", { name: /^Incorrect/ });
      await userEvent.click(incorrect);
      expect(await within(observed()).findByRole("alert")).toHaveTextContent("Feedback wasn't saved");
      expect(incorrect).toHaveAttribute("aria-pressed", "false");
      expect(incorrect).toHaveTextContent(/^Incorrect$/);
      expect(within(observed()).queryByText(/Marked incorrect/)).toBeNull();
    });

    it("notes that an incorrect finding stays visible", async () => {
      mockFetch({ "PUT /v1/findings/f-1/feedback": () => ({ useful: 0, incorrect: 1, mine: "incorrect" }) });
      render(<Harness findings={findings} events={events} layout="stream" onCite={() => {}} />);
      await userEvent.click(within(observed()).getByRole("button", { name: /^Incorrect/ }));
      expect(await within(observed()).findByText("Marked incorrect. Recorded for review; the finding stays visible.")).toBeInTheDocument();
      expect(within(observed()).getByText(/first fired at 14:09/)).toBeInTheDocument();
    });

    it("the next-check box sends done and strikes the check through", async () => {
      const fetch = mockFetch({
        "PUT /v1/findings/f-5/check": () => ({ done: true, done_by: { id: "u-1", display_name: "Ama Owusu" }, done_at: "2026-11-09T14:20:00Z" }),
      });
      render(<Harness findings={findings} events={events} layout="grouped" onCite={() => {}} />);
      const checks = screen.getByRole("region", { name: "Check next" });
      const box = within(checks).getByRole("checkbox", { name: "Mark done" });
      expect(box).toHaveAttribute("aria-checked", "false");
      await userEvent.click(box);
      expect(body(fetch.mock.calls[0])).toEqual({ done: true });
      await waitFor(() => expect(box).toHaveAttribute("aria-checked", "true"));
      const text = within(checks).getByText(/Inspect payment-service logs/);
      expect(text).toHaveClass("ftext-done");
      expect(text).toHaveAttribute("title", "Done by Ama Owusu at 14:20:00 UTC");
    });

    it("unticking a done check sends done: false", async () => {
      const fetch = mockFetch({ "PUT /v1/findings/f-5/check": () => ({ done: false, done_by: null, done_at: null }) });
      const done = findings.map((f) =>
        f.kind === "next_check" ? { ...f, check: { done: true, done_by: { id: "u-1", display_name: "Ama Owusu" }, done_at: "2026-11-09T14:20:00Z" } } : f,
      );
      render(<Harness findings={done} events={events} layout="grouped" onCite={() => {}} />);
      const box = screen.getByRole("checkbox", { name: "Done; mark not done" });
      await userEvent.click(box);
      expect(body(fetch.mock.calls[0])).toEqual({ done: false });
      await waitFor(() => expect(box).toHaveAttribute("aria-checked", "false"));
      expect(screen.getByText(/Inspect payment-service logs/)).not.toHaveClass("ftext-done");
    });

    it("rolls a check back when it can't be saved", async () => {
      mockFetch({ "PUT /v1/findings/f-5/check": () => apiError(503, "database_unavailable") });
      render(<Harness findings={findings} events={events} layout="grouped" onCite={() => {}} />);
      const checks = screen.getByRole("region", { name: "Check next" });
      await userEvent.click(within(checks).getByRole("checkbox"));
      expect(await within(checks).findByRole("alert")).toHaveTextContent("Couldn't update the check");
      expect(within(checks).getByRole("checkbox")).toHaveAttribute("aria-checked", "false");
    });
  });
});

