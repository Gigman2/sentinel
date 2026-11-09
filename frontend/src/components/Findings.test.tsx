import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { referenceIncident, referenceTimeline } from "../test-fixtures";
import { Findings } from "./Findings";

const findings = referenceIncident().analysis.latest_revision!.findings;
const events = new Map(referenceTimeline().items.map((e) => [e.event_id, e]));

describe("Findings", () => {
  it("groups each kind under its heading, in order", () => {
    render(<Findings findings={findings} events={events} layout="grouped" onCite={() => {}} />);
    const headings = screen.getAllByRole("heading", { level: 3 }).map((h) => h.textContent);
    expect(headings).toEqual(["Observed", "Hypothesis", "Gaps", "Check next"]);
    const observed = screen.getByRole("region", { name: "Observed" });
    expect(within(observed).getAllByRole("listitem")).toHaveLength(2);
    const gaps = screen.getByRole("region", { name: "Gaps" });
    expect(within(gaps).getByText(/No matching error events/)).toBeInTheDocument();
  });

  it("tags every finding with its kind in words, not only colour", () => {
    render(<Findings findings={findings} events={events} layout="grouped" onCite={() => {}} />);
    // Heading plus one tag per finding.
    expect(within(screen.getByRole("region", { name: "Observed" })).getAllByText("Observed")).toHaveLength(3);
    expect(within(screen.getByRole("region", { name: "Hypothesis" })).getAllByText("Hypothesis")).toHaveLength(2);
    expect(within(screen.getByRole("region", { name: "Gaps" })).getByText("Gap")).toBeInTheDocument();
  });

  it("marks hypotheses as possible and shows their limitations", () => {
    render(<Findings findings={findings} events={events} layout="grouped" onCite={() => {}} />);
    const hyp = screen.getByRole("region", { name: "Hypothesis" });
    expect(within(hyp).getByText("Possible:")).toBeInTheDocument();
    expect(within(hyp).getByText("Timing alone cannot establish causation.")).toBeInTheDocument();
    expect(within(hyp).getByText("Rests on")).toBeInTheDocument();
  });

  it("hides empty groups", () => {
    render(<Findings findings={findings.filter((f) => f.kind !== "unknown")} events={events} layout="grouped" onCite={() => {}} />);
    expect(screen.queryByRole("region", { name: "Gaps" })).toBeNull();
  });

  it("stream layout has no group headings but still tags every finding", () => {
    render(<Findings findings={findings} events={events} layout="stream" onCite={() => {}} />);
    expect(screen.queryAllByRole("heading", { level: 3 })).toHaveLength(0);
    expect(screen.getAllByRole("listitem")).toHaveLength(5);
    expect(screen.getByText("Check")).toBeInTheDocument();
  });

  it("citation buttons report the cited event", async () => {
    const onCite = vi.fn();
    render(<Findings findings={findings} events={events} layout="grouped" onCite={onCite} />);
    await userEvent.click(screen.getAllByRole("button", { name: /Deployment · 14:02/ })[0]);
    expect(onCite).toHaveBeenCalledWith("evt-deploy");
  });

  it("labels a citation whose event is not loaded", () => {
    const f = [{ ...findings[0], evidence_ids: ["evt-missing"] }];
    render(<Findings findings={f} events={events} layout="grouped" onCite={() => {}} />);
    expect(screen.getByRole("button", { name: /Event/ })).toBeInTheDocument();
  });
});
