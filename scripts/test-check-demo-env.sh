#!/usr/bin/env bash
# Tests for scripts/check-demo-env.sh: `make up` stops, naming what to add,
# when .env lacks the demo tokens.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
cp "$here/../.env.example" "$tmp/.env.example"

fail=0
run() { # name, want exit status, .env contents
  printf '%s' "$3" > "$tmp/.env"
  set +e
  out="$("$here/check-demo-env.sh" "$tmp" 2>&1)"
  status=$?
  set -e
  if [[ "$status" == "$2" ]]; then echo "ok   $1"; else echo "FAIL $1: exit $status, want $2: $out"; fail=1; fi
}

run "the .env.example tokens pass" 0 "$(cat "$tmp/.env.example")"
run "exported tokens pass" 0 $'export SEED_ADMIN_TOKEN=a\nexport SEED_MONITOR_TOKEN=b\nexport SEED_DEPLOY_TOKEN=c\n'
run "an old .env with commented-out tokens stops" 1 $'PORT=9020\n# SEED_ADMIN_TOKEN=\n'
run "an empty token stops" 1 $'SEED_ADMIN_TOKEN=\nSEED_MONITOR_TOKEN=b\nSEED_DEPLOY_TOKEN=c\n'

printf 'PORT=9020\n' > "$tmp/.env"
msg="$("$here/check-demo-env.sh" "$tmp" 2>&1 || true)"
if grep -q '^SEED_MONITOR_TOKEN=sk_int_' <<<"$msg"; then echo "ok   the message shows the lines to add"; else echo "FAIL message: $msg"; fail=1; fi

exit "$fail"
