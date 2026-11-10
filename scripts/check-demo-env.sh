#!/usr/bin/env bash
# Used by `make up`: the stack is seeded with the SEED_* tokens in .env, and
# `make demo` and the UI need to know them. A .env made from an older
# .env.example lacks them; say what to add instead of starting a stack
# whose tokens only appear in the container logs.
set -euo pipefail
cd "${1:-$(dirname "$0")/..}" # the repo root, or a directory to check (tests)

missing=()
for key in SEED_ADMIN_TOKEN SEED_MONITOR_TOKEN SEED_DEPLOY_TOKEN; do
  grep -Eq "^[[:space:]]*(export[[:space:]]+)?${key}=[^[:space:]#]" .env 2>/dev/null || missing+=("$key")
done
if ((${#missing[@]})); then
  echo "error: .env has no ${missing[*]}." >&2
  echo "Add these lines from .env.example to .env, then run make up again:" >&2
  for key in "${missing[@]}"; do grep -E "^${key}=" .env.example >&2; done
  exit 1
fi
