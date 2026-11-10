# Frontend

The engineer view, built to the "Sentinel · Ledger" design (`docs/design/sentinel-ledger.html`). It covers sign-in, the incident list, incident detail (findings, activity timeline, properties, the analysis status, resolve) and integrations. It is React 18 + Vite + TypeScript with plain CSS; there is no component library.

## Run it

```sh
make run              # from the repo root: PostgreSQL, the API on :8080 and the worker
cd frontend
npm install
npm run dev           # http://localhost:5173, proxying /v1 to the API
```

Sign in with the admin token printed by the first `make run` (or the `SEED_ADMIN_TOKEN` in `.env`). The dev server proxies to the API's `PORT`, taken from the environment first and then the repo's `.env`, the same rule as `make run`. The default is 8080. If the API can't be reached, sign-in says so instead of showing a bare 500.

## Scripts

- `npm test`: Vitest + Testing Library (jsdom).
- `npm run build`: type-check, then build to `dist/`.

## Rules the code keeps

- **Text and links:** server text is rendered as text, never as HTML. Source links pass through `safeHref` (http/https only).
- **Wording:** `src/wording.test.ts` fails if any file under `src/` contains the banned wording or raw HTML injection.
- **Times:** shown in UTC, with the full timestamp on hover.
- **Polling:** while an incident's analysis is pending or running, the detail page re-reads it every 3 s. Polling stops on a terminal state, when you navigate away, or when the token is rejected.
