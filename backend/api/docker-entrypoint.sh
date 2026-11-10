#!/bin/sh
set -e
sentinel-api migrate
sentinel-api seed
if [ "${SEED_DEMO_DATA:-1}" != 0 ]; then
  sentinel-api demo-data &
fi
exec sentinel-api serve
