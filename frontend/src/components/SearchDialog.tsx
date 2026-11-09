import { useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { ApiError, apiFetch, describeError } from "../api";
import { shortId } from "../format";
import type { IncidentListItem, IncidentPage } from "../types";
import { StatusIcon } from "./StatusIcon";

const MAX_RUNES = 200;
const DEBOUNCE_MS = 200;

/** Finds incidents in the project by title (⌘K / Ctrl-K). Enter opens the
 * highlighted result; arrow keys move the highlight; Escape closes. */
export function SearchDialog({ projectId, onClose }: { projectId: string; onClose: () => void }) {
  const [text, setText] = useState("");
  const [results, setResults] = useState<IncidentListItem[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState(0);
  const input = useRef<HTMLInputElement>(null);
  const q = [...text.trim()].slice(0, MAX_RUNES).join("");

  useEffect(() => {
    input.current?.focus();
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  useEffect(() => {
    setSelected(0);
    if (!q) {
      setResults(null);
      setError(null);
      return;
    }
    let active = true;
    const timer = setTimeout(() => {
      const params = new URLSearchParams({ q, limit: "10" });
      apiFetch<IncidentPage>("GET", `/v1/projects/${projectId}/incidents?${params}`)
        .then((page) => {
          if (!active) return;
          setResults(page.items);
          setError(null);
        })
        .catch((e) => {
          if (!active || (e instanceof ApiError && e.status === 401)) return;
          setError(describeError(e));
        });
    }, DEBOUNCE_MS);
    return () => {
      active = false;
      clearTimeout(timer);
    };
  }, [q, projectId]);

  function open(item: IncidentListItem) {
    window.location.hash = `#/incidents/${item.id}`;
    onClose();
  }

  function onKeyDown(e: ReactKeyboardEvent) {
    if (!results || results.length === 0) return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setSelected((i) => (i + 1) % results.length);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setSelected((i) => (i - 1 + results.length) % results.length);
    } else if (e.key === "Enter") {
      e.preventDefault();
      open(results[selected]);
    }
  }

  return (
    <div className="overlay" onClick={onClose}>
      <div role="dialog" aria-modal="true" aria-label="Search incidents" className="dialog search-dialog" onClick={(e) => e.stopPropagation()}>
        <label className="search-input">
          <svg width="15" height="15" viewBox="0 0 16 16" aria-hidden="true">
            <circle cx="7" cy="7" r="4.5" fill="none" stroke="currentColor" strokeWidth="1.4" />
            <path d="M10.5 10.5 14 14" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
          </svg>
          <span className="visually-hidden">Search incidents by title</span>
          <input
            ref={input}
            type="search"
            value={text}
            maxLength={MAX_RUNES * 2}
            placeholder="Search incidents by title"
            onChange={(e) => setText(e.target.value)}
            onKeyDown={onKeyDown}
            role="combobox"
            aria-expanded={!!results && results.length > 0}
            aria-controls="search-results"
            aria-activedescendant={results && results.length > 0 ? `search-${results[selected]?.id}` : undefined}
          />
          <span className="mono nav-kbd">Esc</span>
        </label>
        {error && <div className="search-note error-text">{error}</div>}
        {!error && q && results?.length === 0 && <div className="search-note">No incidents match “{q}”.</div>}
        {!error && !q && <div className="search-note">Type part of an incident title.</div>}
        {results && results.length > 0 && (
          <ul id="search-results" role="listbox" aria-label="Matching incidents" className="search-results">
            {results.map((r, i) => (
              <li key={r.id} role="presentation">
                <a
                  id={`search-${r.id}`}
                  role="option"
                  aria-selected={i === selected}
                  href={`#/incidents/${r.id}`}
                  className="search-result"
                  onClick={onClose}
                  onMouseEnter={() => setSelected(i)}
                >
                  <span className="mono muted small">{shortId(r.id, r.number)}</span>
                  <span className="search-title">{r.title}</span>
                  <StatusIcon status={r.status} />
                </a>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
