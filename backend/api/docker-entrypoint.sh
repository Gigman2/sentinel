#!/bin/sh
# Migrate, seed, add demo incidents in the background unless SEED_DEMO_DATA
# is 0, false, no, off or empty, then serve. Compose runs this under an init
# process (init: true), which adopts and reaps demo-data and forwards signals.
set -e
sentinel-api migrate
sentinel-api seed
case "$(printf '%s' "${SEED_DEMO_DATA-1}" | tr '[:upper:]' '[:lower:]')" in
  0 | false | no | off | "") ;;
  *) (sentinel-api demo-data &) ;; # a subshell, so init (not the server) is its parent and reaps it
esac
exec sentinel-api serve
