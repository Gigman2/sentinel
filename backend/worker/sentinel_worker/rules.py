"""Deterministic, evidence-cited rules over an incident's linked events.

Findings are cautious by construction: observations and hypotheses cite the
events they rest on, hypotheses carry their limitations, and nothing claims a
cause. The window constants match the API's evidence window
(incidents.WindowBefore / WindowAfter).
"""

from datetime import UTC, datetime, timedelta

from sentinel_worker.models import EvidenceEvent, Finding, IncidentContext

WINDOW_BEFORE = timedelta(minutes=30)
WINDOW_AFTER = timedelta(minutes=15)

TRIGGER_REASONS = ("trigger", "repeat_trigger")


def _t(value: datetime) -> str:
    """Format a time as HH:MM in UTC."""
    return value.astimezone(UTC).strftime("%H:%M UTC")


def _version(event: EvidenceEvent) -> str:
    return str(event.details.get("version") or "unknown")


def analyze(ctx: IncidentContext) -> list[Finding]:
    """Return the findings for an incident, in a fixed order: observations,
    hypotheses, unknowns, next checks; within a kind, rule order; within a
    rule, the order the events occurred.

    Raises ValueError if no trigger or repeat-trigger event is linked.
    """
    events = sorted(ctx.events, key=lambda e: (e.occurred_at, e.id))
    triggers = [e for e in events if e.link_reason in TRIGGER_REASONS]
    if not triggers:
        raise ValueError(f"incident {ctx.incident_id} has no trigger event")
    first_trigger = next((e for e in triggers if e.link_reason == "trigger"), triggers[0])
    deployments = [e for e in events if e.type == "deployment.completed"]
    errors = [e for e in events if e.type == "error.observed"]

    component = ctx.component_slug
    first_seen = ctx.first_seen_at
    findings: list[Finding] = []

    # Observations.
    findings.append(Finding(
        "observation", "trigger_alert",
        f"{component} alert '{first_trigger.summary}' first fired at {_t(first_seen)}.",
        tuple(e.id for e in triggers)))
    for d in deployments:
        findings.append(Finding(
            "observation", "deployment_in_window",
            f"{component} version {_version(d)} was deployed at {_t(d.occurred_at)}.",
            (d.id,)))
    if errors:
        findings.append(Finding(
            "observation", "errors_observed",
            f"{len(errors)} error event(s) were observed for {component} "
            f"between {_t(errors[0].occurred_at)} and {_t(errors[-1].occurred_at)}.",
            tuple(e.id for e in errors)))

    # Hypotheses.
    for d in deployments:
        if first_seen - WINDOW_BEFORE <= d.occurred_at <= first_seen:
            findings.append(Finding(
                "hypothesis", "deployment_near_onset",
                f"The {component} deployment of version {_version(d)} at {_t(d.occurred_at)} "
                f"may be related to the alert at {_t(first_seen)}.",
                (d.id, first_trigger.id),
                limitations="Timing alone cannot establish causation."))
    if errors and errors[0].occurred_at < first_seen:
        findings.append(Finding(
            "hypothesis", "error_before_alert",
            f"Errors began at {_t(errors[0].occurred_at)}, before the alert fired at {_t(first_seen)}; "
            "the problem may have started earlier than the alert suggests.",
            (errors[0].id, first_trigger.id),
            limitations="Error events show symptoms, not their origin."))

    # Unknowns.
    if not deployments:
        findings.append(Finding(
            "unknown", "no_deployment_evidence",
            f"No deployment information is available for {component} in the investigated window, "
            "so whether a change contributed is unknown.", ()))
    if not errors:
        findings.append(Finding(
            "unknown", "no_error_evidence",
            f"No matching error events were found for {component} in the investigated window.", ()))

    # Next checks.
    if deployments:
        versions = list(dict.fromkeys(_version(d) for d in deployments))
        findings.append(Finding(
            "next_check", "compare_by_version",
            f"Compare affected {component} instances by deployed version ({', '.join(versions)}).", ()))
        findings.append(Finding(
            "next_check", "review_change",
            f"Review what changed in {component} version {_version(deployments[-1])}, "
            "starting with its external integrations.", ()))
    else:
        findings.append(Finding(
            "next_check", "check_change_history",
            f"Check deployment and configuration history for {component}; "
            "Sentinel has no change data for this window.", ()))
    findings.append(Finding(
        "next_check", "inspect_telemetry",
        f"Inspect {component} logs and metrics from {_t(first_seen - WINDOW_BEFORE)} "
        f"to {_t(ctx.last_seen_at + WINDOW_AFTER)}.", ()))
    return findings
