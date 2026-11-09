import { useId, useState, type FormEvent } from "react";
import { ApiError, apiFetch, describeError, setToken } from "../api";
import type { Page, Project } from "../types";

/** No accounts in Phase 1: the person pastes an API token, kept for this
 * browser session only. */
export function SignIn({ onSignedIn }: { onSignedIn: (projects: Project[]) => void }) {
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const id = useId();

  async function submit(e: FormEvent) {
    e.preventDefault();
    const token = value.trim();
    if (!token) return;
    setBusy(true);
    setError(null);
    setToken(token);
    try {
      const page = await apiFetch<Pick<Page<Project>, "items">>("GET", "/v1/projects");
      onSignedIn(page.items);
    } catch (err) {
      setError(err instanceof ApiError && err.status === 401 ? "That token wasn't accepted." : describeError(err));
      setBusy(false);
    }
  }

  return (
    <div className="signin-page">
      <form className="signin" onSubmit={submit}>
        <svg width="28" height="28" viewBox="0 0 20 20" aria-hidden="true">
          <rect width="20" height="20" rx="5" fill="var(--primary)" />
          <path d="M4 11h3l2-5 3 9 2-4h2" fill="none" stroke="var(--primary-fg)" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
        <h1>Sign in to Sentinel</h1>
        <p className="muted-2">
          Paste an API token. The admin token is printed once, when the server first seeds its database (<span className="mono">make run</span>).
        </p>
        <label htmlFor={id} className="field-label">
          API token
        </label>
        <input
          id={id}
          type="password"
          autoComplete="off"
          spellCheck={false}
          className="mono"
          placeholder="sk_usr_…"
          value={value}
          onChange={(e) => setValue(e.target.value)}
        />
        {error && (
          <span className="error-text" role="alert">
            {error}
          </span>
        )}
        <button type="submit" className="btn btn-dark btn-wide" disabled={busy || !value.trim()}>
          {busy ? "Checking…" : "Sign in"}
        </button>
        <span className="muted small">The token is kept for this browser tab only.</span>
      </form>
    </div>
  );
}
