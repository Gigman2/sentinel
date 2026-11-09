#!/usr/bin/env bash
# Tests for scripts/load-env.sh: values already in the environment win over
# .env; .env only fills in what is unset.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

cat > "$tmp/.env" <<'ENV'
# a comment
PORT=9020
DATABASE_URL=postgres://from-dotenv/db?sslmode=disable
ONLY_IN_DOTENV='has spaces and $dollar'
ENV

fail=0
check() { # name, got, want
  if [[ "$2" == "$3" ]]; then echo "ok   $1"; else echo "FAIL $1: got '$2', want '$3'"; fail=1; fi
}

out="$(env -i PATH="$PATH" PORT=18093 DATABASE_URL='postgres://from-env/db?x=1&y=$z' bash -c '
  source "'"$here"'/load-env.sh"
  load_dotenv "'"$tmp"'/.env"
  printf "%s\n" "$PORT" "$DATABASE_URL" "$ONLY_IN_DOTENV"
  bash -c "printf \"%s\n\" \"\$ONLY_IN_DOTENV\""
')"
got=()
while IFS= read -r line; do got+=("$line"); done <<<"$out"
check "environment PORT wins" "${got[0]}" "18093"
check "environment DATABASE_URL wins, special characters intact" "${got[1]}" 'postgres://from-env/db?x=1&y=$z'
check ".env fills unset variables" "${got[2]}" 'has spaces and $dollar'
check ".env variables are exported to child processes" "${got[3]}" 'has spaces and $dollar'

out="$(env -i PATH="$PATH" bash -c 'source "'"$here"'/load-env.sh"; load_dotenv "'"$tmp"'/missing.env"; echo "${PORT:-unset}"')"
check "a missing .env is fine" "$out" "unset"

exit "$fail"
