# Sourced by scripts that read the repo's git-ignored .env.
#
# load_dotenv FILE loads FILE (if it exists) and exports its variables, but
# never overrides a variable that is already exported: the environment wins,
# .env only fills in what is unset. Works with bash 3.2 (macOS).
load_dotenv() {
  local file="$1" saved
  [[ -f "$file" ]] || return 0
  # Plain exported variables, as `export NAME="value"` lines with bash quoting.
  # (Readonly or otherwise special variables are left out; .env can't set them.)
  saved="$(export -p | sed -n 's/^declare -x /export /p')"
  set -a
  # shellcheck disable=SC1090
  source "$file"
  set +a
  eval "$saved"
}
