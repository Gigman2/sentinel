import re

import pytest

from sentinel_worker.rules import analyze
from sentinel_worker.summary import summarize
from test_rules import FIRST_SEEN, deployment, error, make_ctx, trigger


def reference():
    return make_ctx([deployment(), error(), trigger()])


def text(segments) -> str:
    return "".join(s["text"] for s in segments)


def test_reference_summary_exact():
    ctx = reference()
    assert summarize(ctx, analyze(ctx)) == [
        {"text": "The payment-service alert 'Payment errors affecting checkout' first fired at ", "kind": "text"},
        {"text": "14:09", "kind": "time"},
        {"text": ". Version 1.8.4 was deployed at ", "kind": "text"},
        {"text": "14:02", "kind": "time"},
        {"text": ". 1 error event was observed at ", "kind": "text"},
        {"text": "14:07", "kind": "time"},
        {"text": ". Investigate next: compare affected payment-service instances by deployed version (1.8.4).",
         "kind": "text"},
    ]


def test_alert_only_says_cause_unknown():
    ctx = make_ctx([trigger()])
    segments = summarize(ctx, analyze(ctx))
    assert segments[-1] == {"text": "Cause: Unknown.", "kind": "strong"}
    assert "Deployment information unavailable." in text(segments)
    assert "check deployment and configuration history" in text(segments)


def test_several_errors_give_a_range():
    ctx = make_ctx([error("e1", -5), error("e2", -1), trigger()])
    segments = summarize(ctx, analyze(ctx))
    assert "3 error events" not in text(segments)
    assert "2 error events were observed between 14:04 and 14:08." in text(segments)


SCENARIOS = {
    "reference": [deployment(), error(), trigger()],
    "alert only": [trigger()],
    "two deployments": [deployment("d1", -20, "1.8.3"), deployment("d2", -5), trigger()],
    "errors only": [error("e1", -5), error("e2", -1), trigger()],
    "repeat alerts": [trigger(), trigger("alert-2", 3, reason="repeat_trigger")],
}


def scenario_ctx(events):
    """last_seen follows the latest trigger, as grouping sets it."""
    latest = max(e.occurred_at for e in events if e.type == "alert.triggered")
    return make_ctx(events, last_seen_minutes=(latest - FIRST_SEEN).total_seconds() / 60)


@pytest.mark.parametrize("events", SCENARIOS.values(), ids=SCENARIOS.keys())
def test_every_time_is_in_a_finding(events):
    ctx = scenario_ctx(events)
    findings = analyze(ctx)
    times = [s["text"] for s in summarize(ctx, findings) if s["kind"] == "time"]
    assert times, "a summary always cites the alert time"
    for t in times:
        assert re.fullmatch(r"\d\d:\d\d", t)
        assert any(f"{t} UTC" in f.text for f in findings), f"{t} is in no finding"


@pytest.mark.parametrize("events", SCENARIOS.values(), ids=SCENARIOS.keys())
def test_wording(events):
    ctx = scenario_ctx(events)
    lowered = text(summarize(ctx, analyze(ctx))).lower()
    for banned in ("root cause", "caused", "confirmed", "healthy"):
        assert banned not in lowered


def test_deterministic():
    ctx = reference()
    assert summarize(ctx, analyze(ctx)) == summarize(ctx, list(analyze(ctx)))


def test_only_from_findings():
    """A deployment in the context that no finding cites is not summarized."""
    ctx = reference()
    findings = [f for f in analyze(ctx) if f.rule_id != "deployment_in_window"]
    segments = summarize(ctx, findings)
    assert "was deployed at" not in text(segments)
    assert [s["text"] for s in segments if s["kind"] == "time"] == ["14:09", "14:07"]


def test_no_trigger_observation_gives_no_summary():
    ctx = reference()
    assert summarize(ctx, [f for f in analyze(ctx) if f.rule_id != "trigger_alert"]) == []
