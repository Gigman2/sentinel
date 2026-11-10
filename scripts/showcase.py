#!/usr/bin/env python3
"""Fill a running Sentinel with a realistic set of incidents to explore.

    python3 scripts/showcase.py        # or: make showcase

Everything goes through the public API, and the worker analyses it as it
would real data. It adds components, integration mappings and dependencies
next to the seeded ones, then sends these stories:

    checkout-api          critical, open: a deployment, errors, then an alert that
                          repeats and escalates from warning; a finding marked
                          useful, a check marked done, and a manual reanalysis
    payment-service       error, open: the deployment arrives after the first
                          analysis, so revision 2 and its history appear
    ledger-service        error, resolved by its monitor's alert.resolved
    notification-service  warning, resolved by an engineer with a cause note
    search-api            info, open: an alert with no other evidence, and a
                          finding marked incorrect
    search-api            an alert.resolved that matched no open incident
    payment-service       critical, six days ago, resolved (for the time filters)

Event IDs are fixed, so running it again resends the same events (answered as
replays) and changes nothing. Settings are the same as scripts/demo.py.
"""

from __future__ import annotations

import datetime as dt
import sys
import time

from demo import Client, Fail, iso, load_settings

ENV = "production"

# slug, display name, kind
COMPONENTS = [
    ("ledger-service", "Ledger service", "internal"),
    ("notification-service", "Notification service", "internal"),
    ("search-api", "Search API", "internal"),
    ("bank-gateway", "Bank gateway", "external"),
]
# Registered "calls" edges, besides the seeded checkout-api → payment-service
# → payment-provider.
DEPENDENCIES = [
    ("payment-service", "ledger-service"),
    ("ledger-service", "bank-gateway"),
    ("checkout-api", "notification-service"),
]


class Showcase:
    def __init__(self, client: Client):
        self.c = client
        self.now = dt.datetime.now(dt.timezone.utc).replace(microsecond=0)
        projects = client.get("/v1/projects")["items"]
        project = next((p for p in projects if p["slug"] == "uppsel-production"), None)
        if project is None:
            raise Fail("project uppsel-production not found; was the stack seeded?")
        self.project = project["id"]
        integrations = {i["name"]: i["id"] for i in client.get(f"/v1/projects/{self.project}/integrations")["items"]}
        self.monitor, self.deploys = integrations["monitor"], integrations["deploys"]

    # Setup ---------------------------------------------------------------

    def admin(self, path: str, body: dict, ok_codes: tuple[str, ...] = ()) -> None:
        """POST as the admin; a 409 with one of ok_codes means it already exists."""
        status, out = self.c.call("POST", path, self.c.s["admin"], body)
        if status == 201 or (status == 409 and isinstance(out, dict) and out.get("error", {}).get("code") in ok_codes):
            return
        raise Fail(f"POST {path}: HTTP {status} {out}")

    def setup(self) -> None:
        base = f"/v1/projects/{self.project}"
        for slug, name, kind in COMPONENTS:
            self.admin(f"{base}/components", {"slug": slug, "display_name": name, "environment": ENV, "kind": kind},
                       ok_codes=("component_exists",))
        ids = {c["slug"]: c["id"] for c in self.c.get(f"{base}/components")["items"] if c["environment"] == ENV}
        # Each component is known to both integrations by its own slug.
        for slug in ("checkout-api", "payment-service", "ledger-service", "notification-service", "search-api"):
            for integration in (self.monitor, self.deploys):
                self.admin(f"/v1/integrations/{integration}/mappings",
                           {"external_component_id": slug, "environment": ENV, "component_id": ids[slug]},
                           ok_codes=("mapping_exists",))
        for src, dst in DEPENDENCIES:
            self.admin(f"{base}/dependencies", {"from": ids[src], "to": ids[dst]}, ok_codes=("duplicate",))
        print(f"components, mappings and {len(DEPENDENCIES)} dependencies in place")

    # Events --------------------------------------------------------------

    def at(self, **delta: float) -> str:
        return iso(self.now - dt.timedelta(**delta))

    def send(self, integration: str, token: str, event: dict) -> dict:
        event = {"schema_version": "1", "environment": ENV, **event}
        return self.c.ingest(integration, token, event, want=(200, 201))

    def alert(self, key: str, component: str, severity: str, summary: str, fingerprint: str, ago: dict,
              resolved: bool = False) -> dict:
        return self.send(self.monitor, self.c.s["monitor"], {
            "source_event_id": f"showcase-{key}", "type": "alert.resolved" if resolved else "alert.triggered",
            "occurred_at": self.at(**ago), "external_component_id": component, "severity": severity,
            "fingerprint": fingerprint, "summary": summary,
            "source_url": f"https://monitor.example/alerts/{fingerprint}"})

    def error(self, key: str, component: str, summary: str, ago: dict, **attributes: object) -> dict:
        return self.send(self.monitor, self.c.s["monitor"], {
            "source_event_id": f"showcase-{key}", "type": "error.observed", "occurred_at": self.at(**ago),
            "external_component_id": component, "severity": "error", "summary": summary,
            "attributes": attributes})

    def deploy(self, key: str, component: str, version: str, sha: str, ago: dict) -> dict:
        return self.send(self.deploys, self.c.s["deploy"], {
            "source_event_id": f"showcase-{key}", "type": "deployment.completed", "deployed_at": self.at(**ago),
            "external_component_id": component, "version": version, "commit_sha": sha,
            "repository": f"uppsel/{component}", "summary": f"Deploy {component} {version}",
            "source_url": f"https://deploys.example/{component}/{version}"})

    # Analysis and engineer actions ------------------------------------------

    def wait(self, incident: str, revisions: int = 1) -> dict:
        deadline = time.monotonic() + 30
        while True:
            analysis = self.c.get(f"/v1/incidents/{incident}")["analysis"]
            if analysis["status"] == "succeeded" and analysis["revision_count"] >= revisions:
                return analysis
            if time.monotonic() > deadline:
                raise Fail(f"analysis of {incident} not done after 30s ({analysis['status']}); is the worker running?")
            time.sleep(0.5)

    def finding(self, analysis: dict, rule_id: str) -> dict | None:
        return next((f for f in analysis["latest_revision"]["findings"] if f["rule_id"] == rule_id), None)

    def put(self, path: str, body: dict) -> None:
        status, out = self.c.call("PUT", path, self.c.s["admin"], body)
        if status != 200:
            raise Fail(f"PUT {path}: HTTP {status} {out}")

    def resolve(self, incident: str, note: str) -> None:
        status, out = self.c.call("POST", f"/v1/incidents/{incident}/resolve", self.c.s["admin"], {"note": note})
        if status not in (200, 409):  # 409: already resolved by an earlier run
            raise Fail(f"resolve {incident}: HTTP {status} {out}")

    def done(self, title: str, incident: str) -> None:
        print(f"  {title:<58} {self.c.s['ui']}/#/incidents/{incident}")

    # Stories -------------------------------------------------------------

    def checkout(self) -> None:
        self.deploy("checkout-deploy", "checkout-api", "2.14.0", "9b2d71e", {"minutes": 47})
        self.error("checkout-err-1", "checkout-api", "CartTotalMismatch", {"minutes": 42}, count=17)
        self.error("checkout-err-2", "checkout-api", "UpstreamTimeout: payment-service", {"minutes": 41}, count=64)
        res = self.alert("checkout-alert-1", "checkout-api", "warning", "Checkout error rate above 2%",
                         "checkout-error-rate", {"minutes": 40})
        incident = res["incident_id"]
        self.alert("checkout-alert-2", "checkout-api", "critical", "Checkout error rate above 10%",
                   "checkout-error-rate", {"minutes": 35})
        analysis = self.wait(incident)
        if hyp := self.finding(analysis, "deployment_near_onset"):
            self.put(f"/v1/findings/{hyp['id']}/feedback", {"verdict": "useful"})
        if check := self.finding(analysis, "compare_by_version"):
            self.put(f"/v1/findings/{check['id']}/check", {"done": True})
        if analysis["revision_count"] == 1:  # once: an engineer asked for a fresh analysis
            status, out = self.c.call("POST", f"/v1/incidents/{incident}/reanalyze", self.c.s["admin"])
            if status not in (200, 201, 202, 409):
                raise Fail(f"reanalyze: HTTP {status} {out}")
            self.wait(incident, revisions=2)
        self.done("checkout-api: escalated, feedback, check done, reanalysed", incident)

    def payments_late_deploy(self) -> None:
        res = self.alert("payments-alert", "payment-service", "error", "Payment authorisation failures",
                         "payment-auth-failures", {"hours": 3})
        incident = res["incident_id"]
        self.wait(incident)
        self.deploy("payments-deploy", "payment-service", "1.9.0", "c41e0aa", {"hours": 3, "minutes": 8})
        self.wait(incident, revisions=2)
        self.done("payment-service: late deployment, revision history", incident)

    def ledger_auto_resolved(self) -> None:
        self.deploy("ledger-deploy", "ledger-service", "4.2.1", "e07f5b3", {"hours": 26, "minutes": 12})
        self.error("ledger-err", "ledger-service", "DeadlockDetected on postings", {"hours": 26, "minutes": 3}, count=9)
        res = self.alert("ledger-alert", "ledger-service", "error", "Ledger posting latency p99 > 2s",
                         "ledger-latency", {"hours": 26})
        self.wait(res["incident_id"])
        self.alert("ledger-resolved", "ledger-service", "error", "Ledger posting latency recovered",
                   "ledger-latency", {"hours": 25, "minutes": 20}, resolved=True)
        self.done("ledger-service: resolved by its monitor", res["incident_id"])

    def notifications_manual(self) -> None:
        res = self.alert("notify-alert", "notification-service", "warning", "Order emails delayed > 10 min",
                         "notify-delay", {"days": 2})
        self.wait(res["incident_id"])
        self.resolve(res["incident_id"], "SMTP provider throttled us after the sender limit was lowered in their "
                                         "console. Limit restored; queue drained by 14:20.")
        self.done("notification-service: resolved by an engineer with a note", res["incident_id"])

    def search_quiet(self) -> None:
        res = self.alert("search-alert", "search-api", "info", "Search index lag above 5 minutes",
                         "search-index-lag", {"minutes": 12})
        analysis = self.wait(res["incident_id"])
        if gap := self.finding(analysis, "no_error_evidence"):
            self.put(f"/v1/findings/{gap['id']}/feedback", {"verdict": "incorrect"})
        # A resolution for an alert that never opened an incident.
        self.alert("search-stale-resolved", "search-api", "warning", "Search stale results recovered",
                   "search-stale-results", {"minutes": 5}, resolved=True)
        self.done("search-api: evidence gaps, finding marked incorrect", res["incident_id"])

    def payments_last_week(self) -> None:
        self.deploy("old-deploy", "payment-service", "1.8.0", "77aa310", {"days": 6, "minutes": 9})
        res = self.alert("old-alert", "payment-service", "critical", "Payment provider timeouts",
                         "payment-provider-timeouts", {"days": 6})
        self.wait(res["incident_id"])
        self.alert("old-resolved", "payment-service", "critical", "Payment provider timeouts recovered",
                   "payment-provider-timeouts", {"days": 6, "minutes": -50}, resolved=True)
        self.done("payment-service: six days ago, resolved", res["incident_id"])


def main() -> int:
    try:
        show = Showcase(Client(load_settings()))
        print(f"API {show.c.s['url']}")
        show.setup()
        for story in (show.payments_last_week, show.notifications_manual, show.ledger_auto_resolved,
                      show.payments_late_deploy, show.search_quiet, show.checkout):
            story()
    except Fail as e:
        print(f"FAIL {e}")
        return 1
    print(f"Done. Open {show.c.s['ui']} and sign in with SEED_ADMIN_TOKEN from .env.")
    print("To see an analysis waiting or running: docker compose stop worker, send an alert, then start it.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
