#!/usr/bin/env bash
# Stops what start.sh started.
dir=${E2E_DIR:?set E2E_DIR}
for p in app fakegithub; do
  [ -f "$dir/$p.pid" ] && kill "$(cat "$dir/$p.pid")" 2>/dev/null; rm -f "$dir/$p.pid"
done
exit 0
