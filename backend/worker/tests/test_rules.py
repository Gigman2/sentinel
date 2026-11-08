from datetime import UTC, datetime, timedelta, timezone

import pytest

from sentinel_worker.models import EvidenceEvent, Finding, IncidentContext
from sentinel_worker.rules import analyze

FIRST_SEEN = datetime(2026, 11, 8, 14, 9, tzinfo=UTC)


def at(minutes: float) -> datetime:
    """A time relative to FIRST_SEEN."""
    return FIRST_SEEN + timedelta(minutes=minutes)


def trigger(id="alert-1", minutes=0, summary="Payment errors affecting checkout", reason="trigger"):
    return EvidenceEvent(id=id, type="alert.triggered", occurred_at=at(minutes), summary=summary,
                         severity="critical", link_reason=reason, details={})


def deployment(id="deploy-1", minutes=-7, version="1.8.4"):
    details = {} if version is None else {"version": version}
    return EvidenceEvent(id=id, type="deployment.completed", occurred_at=at(minutes),
                         summary=f"Deploy payment-service {version}", severity=None,
                         link_reason="same_component_time_window", details=details)


def error(id="error-1", minutes=-2):
    return EvidenceEvent(id=id, type="error.observed", occurred_at=at(minutes), summary="PaymentGatewayTimeout",
                         severity="error", link_reason="same_component_time_window", details={})


def make_ctx(events, last_seen_minutes=0) -> IncidentContext:
    return IncidentContext(incident_id="inc-1", component_slug="payment-service", environment="production",
                           first_seen_at=FIRST_SEEN, last_seen_at=at(last_seen_minutes), events=tuple(events))


def by_rule(findings: list[Finding]) -> dict[str, list[Finding]]:
    out: dict[str, list[Finding]] = {}
    for f in findings:
        out.setdefault(f.rule_id, []).append(f)
    return out


def test_reference_incident():
    findings = analyze(make_ctx([deployment(), error(), trigger()]))

    assert [f.rule_id for f in findings] == [
        "trigger_alert", "deployment_in_window", "errors_observed",
        "deployment_near_onset", "error_before_alert",
        "compare_by_version", "review_change", "inspect_telemetry",
    ]
    rules = by_rule(findings)
    assert rules["trigger_alert"][0].text == (
        "payment-service alert 'Payment errors affecting checkout' first fired at 14:09 UTC.")
    assert rules["trigger_alert"][0].evidence_ids == ("alert-1",)
    assert rules["deployment_in_window"][0].text == "payment-service version 1.8.4 was deployed at 14:02 UTC."
    assert rules["errors_observed"][0].text == (
        "1 error event(s) were observed for payment-service between 14:07 UTC and 14:07 UTC.")

    hypothesis = rules["deployment_near_onset"][0]
    assert hypothesis.kind == "hypothesis"
    assert hypothesis.text == (
        "The payment-service deployment of version 1.8.4 at 14:02 UTC may be related to the alert at 14:09 UTC.")
    assert hypothesis.limitations == "Timing alone cannot establish causation."
    assert hypothesis.evidence_ids == ("deploy-1", "alert-1")

    assert rules["error_before_alert"][0].text == (
        "Errors began at 14:07 UTC, before the alert fired at 14:09 UTC; "
        "the problem may have started earlier than the alert suggests.")
    assert rules["error_before_alert"][0].limitations == "Error events show symptoms, not their origin."
    assert rules["error_before_alert"][0].evidence_ids == ("error-1", "alert-1")

    assert rules["compare_by_version"][0].text == "Compare affected payment-service instances by deployed version (1.8.4)."
    assert rules["review_change"][0].text == (
        "Review what changed in payment-service version 1.8.4, starting with its external integrations.")
    assert rules["inspect_telemetry"][0].text == "Inspect payment-service logs and metrics from 13:39 UTC to 14:24 UTC."
    assert all(f.evidence_ids == () for f in findings if f.kind == "next_check")


def test_alert_only_is_honest():
    findings = analyze(make_ctx([trigger()]))

    assert [(f.kind, f.rule_id) for f in findings] == [
        ("observation", "trigger_alert"),
        ("unknown", "no_deployment_evidence"),
        ("unknown", "no_error_evidence"),
        ("next_check", "check_change_history"),
        ("next_check", "inspect_telemetry"),
    ]
    rules = by_rule(findings)
    assert rules["no_deployment_evidence"][0].text == (
        "No deployment information is available for payment-service in the investigated window, "
        "so whether a change contributed is unknown.")
    assert rules["no_error_evidence"][0].text == (
        "No matching error events were found for payment-service in the investigated window.")
    assert rules["check_change_history"][0].text == (
        "Check deployment and configuration history for payment-service; Sentinel has no change data for this window.")


def test_two_deployments_two_candidates():
    findings = analyze(make_ctx([
        deployment("deploy-b", minutes=-5, version="1.8.5"),
        deployment("deploy-a", minutes=-20, version="1.8.4"),
        trigger(),
    ]))
    rules = by_rule(findings)

    # Within a rule, events in the order they occurred.
    assert [f.evidence_ids for f in rules["deployment_in_window"]] == [("deploy-a",), ("deploy-b",)]
    assert [f.evidence_ids for f in rules["deployment_near_onset"]] == [("deploy-a", "alert-1"), ("deploy-b", "alert-1")]
    assert rules["compare_by_version"][0].text.endswith("(1.8.4, 1.8.5).")
    assert "version 1.8.5," in rules["review_change"][0].text


def test_deployment_after_alert_not_hypothesis():
    findings = analyze(make_ctx([trigger(), deployment(minutes=1)], last_seen_minutes=0))
    rules = by_rule(findings)

    assert "deployment_in_window" in rules
    assert "deployment_near_onset" not in rules


def test_window_edges_inclusive():
    findings = analyze(make_ctx([
        deployment("deploy-edge", minutes=-30),
        deployment("deploy-onset", minutes=0),
        deployment("deploy-early", minutes=-30.02),
        trigger(),
    ]))
    candidates = [f.evidence_ids[0] for f in by_rule(findings)["deployment_near_onset"]]

    assert candidates == ["deploy-edge", "deploy-onset"]


def test_duplicate_versions_deduplicated():
    findings = analyze(make_ctx([
        deployment("d1", minutes=-20, version="1.8.4"),
        deployment("d2", minutes=-10, version="1.8.4"),
        deployment("d3", minutes=-5, version=None),
        trigger(),
    ]))
    rules = by_rule(findings)

    assert rules["compare_by_version"][0].text.endswith("(1.8.4, unknown).")
    assert rules["deployment_in_window"][2].text == "payment-service version unknown was deployed at 14:04 UTC."


def test_trigger_summary_from_earliest_trigger_and_repeats_cited():
    findings = analyze(make_ctx([
        trigger("alert-2", minutes=3, summary="Later summary", reason="repeat_trigger"),
        trigger("alert-1", minutes=0, summary="First summary"),
    ], last_seen_minutes=3))
    first = findings[0]

    assert first.rule_id == "trigger_alert"
    assert "'First summary'" in first.text
    assert first.evidence_ids == ("alert-1", "alert-2")


def test_no_trigger_raises():
    with pytest.raises(ValueError):
        analyze(make_ctx([deployment(), error()]))


def test_naive_datetime_rejected():
    with pytest.raises(ValueError):
        EvidenceEvent(id="x", type="error.observed", occurred_at=datetime(2026, 11, 8, 14, 0), summary="",
                      severity=None, link_reason="same_component_time_window", details={})
    with pytest.raises(ValueError):
        IncidentContext(incident_id="i", component_slug="c", environment="production",
                        first_seen_at=datetime(2026, 11, 8, 14, 9), last_seen_at=FIRST_SEEN, events=())


SCENARIOS = {
    "reference": [deployment(), error(), trigger()],
    "alert only": [trigger()],
    "two deployments": [deployment("a", minutes=-20), deployment("b", minutes=-5, version="2.0"), trigger()],
    "late deployment": [trigger(), deployment(minutes=5)],
    "errors only": [error("e1", minutes=-10), error("e2", minutes=1), trigger()],
}


@pytest.mark.parametrize("events", SCENARIOS.values(), ids=SCENARIOS.keys())
def test_citations_and_wording(events):
    ctx = make_ctx(events, last_seen_minutes=0)
    known = {e.id for e in ctx.events}
    for f in analyze(ctx):
        if f.kind in ("observation", "hypothesis"):
            assert f.evidence_ids, f"{f.rule_id} cites no evidence"
        assert set(f.evidence_ids) <= known, f"{f.rule_id} cites unknown events"
        lowered = f"{f.text} {f.limitations or ''}".lower()
        for banned in ("root cause", "caused", "confirmed", "healthy"):
            assert banned not in lowered, f"{f.rule_id}: {banned!r} in {lowered!r}"


def test_deterministic():
    events = [deployment("a", minutes=-20), error(), deployment("b", minutes=-5, version="2.0"), trigger()]
    first = analyze(make_ctx(events))
    assert analyze(make_ctx(list(reversed(events)))) == first
    assert analyze(make_ctx(events)) == first


def test_times_formatted_in_utc_whatever_the_input_zone():
    plus_two = timezone(timedelta(hours=2))
    ctx = IncidentContext(incident_id="inc-1", component_slug="payment-service", environment="production",
                          first_seen_at=FIRST_SEEN.astimezone(plus_two), last_seen_at=FIRST_SEEN.astimezone(plus_two),
                          events=(trigger(),))
    assert analyze(ctx)[0].text.endswith("first fired at 14:09 UTC.")
