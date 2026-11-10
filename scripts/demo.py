#!/usr/bin/env python3
"""Drive Sentinel's Phase 1 demo scenarios against a running stack.

    python3 scripts/demo.py all                 # every scenario (make demo)
    python3 scripts/demo.py happy [--no-wait]   # deployment → error → alert
    python3 scripts/demo.py missing-deployment  # an alert with no deployment
    python3 scripts/demo.py late-deployment     # the deployment arrives after analysis
    python3 scripts/demo.py duplicate           # one alert sent twice

Settings come from the environment, then the repo's .env. The API is
SENTINEL_URL if set, else http://127.0.0.1 on SENTINEL_API_PORT (the Compose
stack), else PORT (`make run`), else 8080. The UI links use SENTINEL_UI_URL,
else SENTINEL_UI_PORT, else 5173. Tokens: SEED_ADMIN_TOKEN,
SEED_MONITOR_TOKEN and SEED_DEPLOY_TOKEN.

Each run registers its own component (demo-<nonce>) and maps it in both
integrations, so earlier runs' events never land in this run's windows.
Standard library only.
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import secrets
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
WAIT_SECONDS = 30


class Fail(Exception):
    """A scenario or setup step failed; the message says why."""


def load_settings() -> dict[str, str]:
    file: dict[str, str] = {}
    env_path = ROOT / ".env"
    if env_path.exists():
        for line in env_path.read_text().splitlines():
            line = line.strip()
            if line and not line.startswith("#") and "=" in line:
                key, value = line.split("=", 1)
                file[key.strip()] = value.strip().strip("'\"")
    get = lambda key, default="": os.environ.get(key) or file.get(key) or default  # noqa: E731
    api_port = get("SENTINEL_API_PORT") or get("PORT") or "8080"
    ui_port = get("SENTINEL_UI_PORT") or "5173"
    settings = {
        "url": get("SENTINEL_URL", f"http://127.0.0.1:{api_port}").rstrip("/"),
        "ui": get("SENTINEL_UI_URL", f"http://127.0.0.1:{ui_port}").rstrip("/"),
        "admin": get("SEED_ADMIN_TOKEN"),
        "monitor": get("SEED_MONITOR_TOKEN"),
        "deploy": get("SEED_DEPLOY_TOKEN"),
    }
    missing = [k for k in ("SEED_ADMIN_TOKEN", "SEED_MONITOR_TOKEN", "SEED_DEPLOY_TOKEN")
               if not settings[k.removeprefix("SEED_").removesuffix("_TOKEN").lower()]]
    if missing:
        raise Fail(f"{', '.join(missing)} not set. Copy the SEED_* lines from .env.example into .env "
                   "(then `make up` again so the stack is seeded with them), or export them.")
    return settings


class Client:
    def __init__(self, settings: dict[str, str]):
        self.s = settings

    def call(self, method: str, path: str, token: str, body: object = None) -> tuple[int, object]:
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(self.s["url"] + path, data=data, method=method, headers={
            "Authorization": f"Bearer {token}", "Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=10) as resp:
                status, raw = resp.status, resp.read()
        except urllib.error.HTTPError as e:
            status, raw = e.code, e.read()
        except (urllib.error.URLError, TimeoutError, OSError) as e:
            raise Fail(f"{method} {path}: cannot reach {self.s['url']} ({getattr(e, 'reason', e)}); is the stack up?")
        try:
            return status, json.loads(raw) if raw else None
        except json.JSONDecodeError:
            raise Fail(f"{method} {path}: HTTP {status}, not JSON: {raw[:120]!r}")

    def get(self, path: str) -> dict:
        status, body = self.call("GET", path, self.s["admin"])
        if status != 200:
            raise Fail(f"GET {path}: HTTP {status} {body}")
        return body  # type: ignore[return-value]

    def post(self, path: str, body: dict) -> dict:
        status, out = self.call("POST", path, self.s["admin"], body)
        if status != 201:
            raise Fail(f"POST {path}: HTTP {status} {out}")
        return out  # type: ignore[return-value]

    def ingest(self, integration: str, token: str, event: dict, want: tuple[int, ...] = (201,)) -> dict:
        status, body = self.call("POST", f"/v1/ingest/{integration}/events", token, event)
        if status not in want:
            raise Fail(f"ingest {event['type']} ({event['source_event_id']}): HTTP {status} {body}")
        return body  # type: ignore[return-value]


def iso(t: dt.datetime) -> str:
    return t.astimezone(dt.timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")


class Demo:
    def __init__(self, client: Client):
        self.c = client
        self.nonce = secrets.token_hex(3)
        projects = client.get("/v1/projects")["items"]
        project = next((p for p in projects if p["slug"] == "uppsel-production"), None)
        if project is None:
            raise Fail("project uppsel-production not found; was the stack seeded?")
        integrations = {i["name"]: i["id"] for i in client.get(f"/v1/projects/{project['id']}/integrations")["items"]}
        if "monitor" not in integrations or "deploys" not in integrations:
            raise Fail(f"integrations monitor and deploys not found (have {sorted(integrations)})")
        self.monitor, self.deploys = integrations["monitor"], integrations["deploys"]

        # A component of this run's own, known to both integrations as its slug.
        self.slug = f"demo-{self.nonce}"
        component = client.post(f"/v1/projects/{project['id']}/components", {
            "slug": self.slug, "display_name": f"Demo {self.nonce}", "environment": "production",
            "description": "Created by scripts/demo.py"})
        for integration in (self.monitor, self.deploys):
            client.post(f"/v1/integrations/{integration}/mappings", {
                "external_component_id": self.slug, "environment": "production", "component_id": component["id"]})
        print(f"API {client.s['url']}; this run's component: {self.slug}")

    # Events. Each scenario gets its own fingerprint, so it opens its own incident.
    def alert(self, scenario: str, at: dt.datetime, n: int = 1) -> dict:
        return {"schema_version": "1", "source_event_id": f"demo-{self.nonce}-{scenario}-alert-{n}",
                "type": "alert.triggered", "occurred_at": iso(at), "external_component_id": self.slug,
                "environment": "production", "severity": "critical", "fingerprint": f"demo-{self.nonce}-{scenario}",
                "summary": "Payment errors affecting checkout",
                "source_url": "https://monitor.example/alerts/demo"}

    def error(self, scenario: str, at: dt.datetime) -> dict:
        return {"schema_version": "1", "source_event_id": f"demo-{self.nonce}-{scenario}-error",
                "type": "error.observed", "occurred_at": iso(at), "external_component_id": self.slug,
                "environment": "production", "severity": "error", "summary": "PaymentGatewayTimeout",
                "attributes": {"count": 42}}

    def deployment(self, scenario: str, at: dt.datetime) -> dict:
        return {"schema_version": "1", "source_event_id": f"demo-{self.nonce}-{scenario}-deploy",
                "type": "deployment.completed", "deployed_at": iso(at), "external_component_id": self.slug,
                "environment": "production", "version": "1.8.4", "commit_sha": "4f1c2e9",
                "repository": "uppsel/payments", "summary": f"Deploy {self.slug} 1.8.4",
                "source_url": "https://deploys.example/payments/1.8.4"}

    def send_alert(self, scenario: str, at: dt.datetime) -> tuple[str, str]:
        res = self.c.ingest(self.monitor, self.c.s["monitor"], self.alert(scenario, at))
        if not res.get("incident_id"):
            raise Fail(f"alert did not open an incident: {res}")
        return res["incident_id"], res["event_id"]

    def wait(self, incident: str, revisions: int = 1) -> dict:
        deadline = time.monotonic() + WAIT_SECONDS
        while True:
            analysis = self.c.get(f"/v1/incidents/{incident}")["analysis"]
            if analysis["status"] == "succeeded" and analysis["revision_count"] >= revisions:
                return analysis
            if analysis["status"] == "failed":
                raise Fail(f"analysis failed: {analysis.get('last_error')}")
            if time.monotonic() > deadline:
                raise Fail(f"analysis not done after {WAIT_SECONDS}s (status {analysis['status']}, "
                           f"{analysis['revision_count']} revisions); is the worker running?")
            time.sleep(0.5)

    def show(self, incident: str, analysis: dict) -> list[dict]:
        timeline = self.c.get(f"/v1/incidents/{incident}/timeline")["items"]
        rev = analysis["latest_revision"] or {}
        print(f"  revision {rev.get('revision_number')}; timeline:")
        for item in timeline:
            print(f"    {item['occurred_at']}  {item['type']:<22} {item['summary']}")
        print("  findings:")
        for f in rev.get("findings", []):
            print(f"    [{f['kind']}] {f['rule_id']}: {f['text']}")
        print(f"  open: {self.c.s['ui']}/#/incidents/{incident}")
        return timeline

    # Scenarios. Each returns normally on PASS and raises Fail otherwise.
    def happy(self, wait: bool = True) -> None:
        base = dt.datetime.now(dt.timezone.utc) - dt.timedelta(hours=6)
        deploy = self.c.ingest(self.deploys, self.c.s["deploy"], self.deployment("happy", base - dt.timedelta(minutes=7)))
        error = self.c.ingest(self.monitor, self.c.s["monitor"], self.error("happy", base - dt.timedelta(minutes=2)))
        incident, alert = self.send_alert("happy", base)
        if not wait:
            print(f"  incident {incident} (not waiting for analysis)")
            print(f"  open: {self.c.s['ui']}/#/incidents/{incident}")
            return
        analysis = self.wait(incident)
        timeline = self.show(incident, analysis)
        ids = {i["event_id"] for i in timeline}
        missing = {deploy["event_id"], error["event_id"], alert} - ids
        if missing:
            raise Fail(f"events missing from the timeline: {sorted(missing)}")
        rules = {f["rule_id"] for f in analysis["latest_revision"]["findings"]}
        if "deployment_near_onset" not in rules:
            raise Fail(f"no deployment_near_onset finding (got {sorted(rules)})")

    def missing_deployment(self) -> None:
        base = dt.datetime.now(dt.timezone.utc) - dt.timedelta(hours=4)
        incident, _ = self.send_alert("missing", base)
        analysis = self.wait(incident)
        self.show(incident, analysis)
        findings = analysis["latest_revision"]["findings"]
        if "no_deployment_evidence" not in {f["rule_id"] for f in findings}:
            raise Fail("no no_deployment_evidence finding")
        if any(f["kind"] == "hypothesis" for f in findings):
            raise Fail("a hypothesis was offered without deployment evidence")

    def late_deployment(self) -> None:
        base = dt.datetime.now(dt.timezone.utc) - dt.timedelta(hours=2)
        incident, _ = self.send_alert("late", base)
        self.wait(incident)
        print("  first analysis done; now the deployment arrives")
        self.c.ingest(self.deploys, self.c.s["deploy"], self.deployment("late", base - dt.timedelta(minutes=7)))
        analysis = self.wait(incident, revisions=2)
        timeline = self.show(incident, analysis)
        if analysis["latest_revision"]["revision_number"] < 2:
            raise Fail("no second revision")
        if not timeline or timeline[0]["type"] != "deployment.completed":
            raise Fail("the late deployment is not first on the timeline")

    def duplicate(self) -> None:
        base = dt.datetime.now(dt.timezone.utc) - dt.timedelta(hours=1)
        event = self.alert("duplicate", base)
        first = self.c.ingest(self.monitor, self.c.s["monitor"], event)
        again = self.c.ingest(self.monitor, self.c.s["monitor"], event, want=(200,))
        print(f"  sent twice: event {first['event_id']}, then {again['event_id']} (replayed={again.get('replayed')})")
        if again["event_id"] != first["event_id"] or not again.get("replayed"):
            raise Fail("the resend was not answered with the original event")
        analysis = self.wait(first["incident_id"])
        self.show(first["incident_id"], analysis)


SCENARIOS = {
    "happy": Demo.happy,
    "missing-deployment": Demo.missing_deployment,
    "late-deployment": Demo.late_deployment,
    "duplicate": Demo.duplicate,
}


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description="Run Sentinel's demo scenarios.")
    parser.add_argument("scenario", choices=[*SCENARIOS, "all"])
    parser.add_argument("--no-wait", action="store_true", help="happy only: print the incident and exit")
    args = parser.parse_args(argv)
    if args.no_wait and args.scenario != "happy":
        parser.error("--no-wait applies to happy only")

    try:
        demo = Demo(Client(load_settings()))
    except Fail as e:
        print(f"FAIL setup: {e}")
        return 1

    names = list(SCENARIOS) if args.scenario == "all" else [args.scenario]
    failed = 0
    for name in names:
        print(f"== {name}")
        try:
            if name == "happy":
                demo.happy(wait=not args.no_wait)
            else:
                SCENARIOS[name](demo)
            print(f"PASS {name}")
        except Fail as e:
            failed += 1
            print(f"FAIL {name}: {e}")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
