#!/bin/sh
# RSS-Tracker Home Assistant add-on entrypoint.
# Reads /data/options.json (written by the Supervisor) and exports env vars.
set -e

# Persistent storage lives in /data (mapped by Home Assistant).
export DB_PATH="/data/rss-tracker.db"

if [ -f /data/options.json ]; then
  # export-options.js prints "export KEY='value'" lines from the options file.
  eval "$(node /usr/local/bin/export-options.js)"
fi

exec dumb-init -- node dist/index.js
