import { useEffect, useId, useRef, useState } from "react";
import { ApiError, apiFetch, describeError } from "../api";

const MAX_RUNES = 4000;

/** Records the cause as the team understands it and resolves the incident. */
export function ResolveDialog({
  incidentId,
  label,
  onClose,
  onResolved,
}: {
  incidentId: string;
  label: string;
  onClose: () => void;
  onResolved: (alreadyResolved: boolean) => void;
}) {
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const area = useRef<HTMLTextAreaElement>(null);
  const titleId = useId();
  const fieldId = useId();
  const runes = [...note].length;
  const ok = note.trim().length > 0 && runes <= MAX_RUNES;

  useEffect(() => {
    area.current?.focus();
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  async function submit() {
    if (!ok || busy) return;
    setBusy(true);
    setError(null);
    try {
      await apiFetch("POST", `/v1/incidents/${incidentId}/resolve`, { note: note.trim() });
      onResolved(false);
    } catch (e) {
      if (e instanceof ApiError && e.code === "already_resolved") {
        onResolved(true);
        return;
      }
      setError(describeError(e));
      setBusy(false);
    }
  }

  return (
    <div className="overlay" onClick={onClose}>
      <div role="dialog" aria-modal="true" aria-labelledby={titleId} className="dialog" onClick={(e) => e.stopPropagation()}>
        <div className="dialog-head">
          <span id={titleId} className="dialog-title">
            Resolve <span className="mono">{label}</span>
          </span>
          <span className="muted-2">
            Record the cause as your team understands it. This is the only place Sentinel labels something as the cause. Analysis findings
            stay as they are.
          </span>
        </div>
        <div className="dialog-body">
          <label htmlFor={fieldId} className="field-label">
            Final cause <span className="req">*</span>
          </label>
          <textarea
            id={fieldId}
            ref={area}
            rows={4}
            value={note}
            onChange={(e) => setNote(e.target.value)}
            placeholder="Written by a person, e.g. what you verified and how"
          />
          <span className="muted small">
            Required. Shown on the incident as “Cause · recorded at resolution”.
            {runes > 3500 && <span className={runes > MAX_RUNES ? " error-text" : ""}>{` ${runes}/${MAX_RUNES}`}</span>}
          </span>
          {error && <span className="error-text">{error}</span>}
        </div>
        <div className="dialog-foot">
          <button type="button" className="btn" onClick={onClose}>
            Cancel
          </button>
          <button type="button" className="btn btn-dark" disabled={!ok || busy} onClick={submit}>
            Resolve incident
          </button>
        </div>
      </div>
    </div>
  );
}
