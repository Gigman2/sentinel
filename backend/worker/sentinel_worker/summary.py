"""A short, deterministic summary of a revision, built only from its findings.

Each sentence restates a finding: the trigger observation, then each
deployment and error observation in order, then the first next check. When
the findings say no deployment information was available, the summary says
so and ends with "Cause: Unknown." Times are separate "time" segments so the
UI can set them apart, and every one of them appears in a finding's text.
Nothing here adds a claim the findings do not make.
"""

from datetime import UTC, datetime
from typing import Literal, TypedDict

from sentinel_worker.models import EvidenceEvent, Finding, IncidentContext
from sentinel_worker.rules import opening_trigger


class Segment(TypedDict):
    text: str
    kind: Literal["text", "time", "strong"]


def _hm(value: datetime) -> str:
    """HH:MM in UTC, as the findings write it (without the zone)."""
    return value.astimezone(UTC).strftime("%H:%M")


def _lower_first(text: str) -> str:
    return text[:1].lower() + text[1:]


class _Builder:
    def __init__(self) -> None:
        self.segments: list[Segment] = []

    def text(self, value: str) -> None:
        if self.segments and self.segments[-1]["kind"] == "text":
            self.segments[-1] = {"text": self.segments[-1]["text"] + value, "kind": "text"}
        else:
            self.segments.append({"text": value, "kind": "text"})

    def time(self, value: datetime) -> None:
        self.segments.append({"text": _hm(value), "kind": "time"})

    def strong(self, value: str) -> None:
        self.segments.append({"text": value, "kind": "strong"})


def summarize(ctx: IncidentContext, findings: list[Finding]) -> list[Segment]:
    """Return the summary segments for these findings (empty if there is no
    trigger observation to start from)."""
    events: dict[str, EvidenceEvent] = {e.id: e for e in ctx.events}
    by_rule: dict[str, list[Finding]] = {}
    for f in findings:
        by_rule.setdefault(f.rule_id, []).append(f)

    trigger = next((f for f in by_rule.get("trigger_alert", []) if f.kind == "observation"), None)
    if trigger is None:
        return []
    out = _Builder()
    # The same alert the trigger finding names, chosen among the events it cites.
    first = opening_trigger([events[i] for i in trigger.evidence_ids if i in events])
    if first is not None and first.summary:
        out.text(f"The {ctx.component_slug} alert '{first.summary}' first fired at ")
    else:
        out.text(f"The {ctx.component_slug} alert first fired at ")
    out.time(ctx.first_seen_at)
    out.text(".")

    for f in (f for f in findings if f.kind == "observation" and f.rule_id in ("deployment_in_window", "errors_observed")):
        cited = sorted((events[i] for i in f.evidence_ids if i in events), key=lambda e: (e.occurred_at, e.id))
        if not cited:
            continue
        if f.rule_id == "deployment_in_window":
            version = str(cited[0].details.get("version") or "unknown")
            out.text(f" Version {version} was deployed at ")
            out.time(cited[0].occurred_at)
            out.text(".")
        else:
            out.text(f" {len(cited)} error event{'' if len(cited) == 1 else 's'} {'was' if len(cited) == 1 else 'were'} observed ")
            if len(cited) == 1:
                out.text("at ")
                out.time(cited[0].occurred_at)
            else:
                out.text("between ")
                out.time(cited[0].occurred_at)
                out.text(" and ")
                out.time(cited[-1].occurred_at)
            out.text(".")

    next_check = next((f for f in findings if f.kind == "next_check"), None)
    if next_check is not None:
        out.text(f" Investigate next: {_lower_first(next_check.text)}")

    if any(f.kind == "unknown" and f.rule_id == "no_deployment_evidence" for f in findings):
        out.text(" Deployment information unavailable. ")
        out.strong("Cause: Unknown.")
    return out.segments
