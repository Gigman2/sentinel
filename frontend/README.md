# Frontend

The engineer view, built to the "Sentinel · Ledger" design (`docs/design/sentinel-ledger.html`). It covers sign-in, the incident list, incident detail (findings, activity timeline, properties, the analysis status, resolve) and integrations. It is React 18 + Vite + TypeScript with plain CSS; there is no component library.

## Run it

```sh
make run              # from the repo root: PostgreSQL, the API on :8080 and the worker
cd frontend
npm install
npm run dev           # http://localhost:5173, proxying /v1 to the API
```

Sign in with the admin token printed by the first `make run` (or the `SEED_ADMIN_TOKEN` in `.env`). If the API listens on another port, start Vite with the same `PORT`, e.g. `PORT=9090 npm run dev`.

## Scripts

- `npm test`: Vitest + Testing Library (jsdom).
- `npm run build`: type-check, then build to `dist/`.

## Rules the code keeps

- **Text and links:** server text is rendered as text, never as HTML. Source links pass through `safeHref` (http/https only).
- **Wording:** `src/wording.test.ts` fails if any file under `src/` contains the banned wording or raw HTML injection.
- **Times:** shown in UTC, with the full timestamp on hover.
- **Polling:** while an incident's analysis is pending or running, the detail page re-reads it every 3 s. Polling stops on a terminal state, when you navigate away, or when the token is rejected.
