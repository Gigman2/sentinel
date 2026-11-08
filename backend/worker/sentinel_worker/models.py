"""Plain data passed between the store and the rules."""

from dataclasses import dataclass
from datetime import datetime
from typing import Any, Literal

FindingKind = Literal["observation", "hypothesis", "unknown", "next_check"]


def _require_aware(name: str, value: datetime) -> None:
    if value.tzinfo is None or value.utcoffset() is None:
        raise ValueError(f"{name} must be timezone-aware, got {value!r}")


@dataclass(frozen=True)
class EvidenceEvent:
    """An event linked to an incident, with the reason it was linked."""

    id: str
    type: str
    occurred_at: datetime
    summary: str
    severity: str | None
    link_reason: str
    details: dict[str, Any]

    def __post_init__(self) -> None:
        _require_aware("occurred_at", self.occurred_at)


@dataclass(frozen=True)
class IncidentContext:
    """Everything the rules may look at for one incident."""

    incident_id: str
    component_slug: str
    environment: str
    first_seen_at: datetime
    last_seen_at: datetime
    events: tuple[EvidenceEvent, ...]

    def __post_init__(self) -> None:
        _require_aware("first_seen_at", self.first_seen_at)
        _require_aware("last_seen_at", self.last_seen_at)


@dataclass(frozen=True)
class Finding:
    """One cautious statement about an incident and the events it cites."""

    kind: FindingKind
    rule_id: str
    text: str
    evidence_ids: tuple[str, ...]
    limitations: str | None = None
