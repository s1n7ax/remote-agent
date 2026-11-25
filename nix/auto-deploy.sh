# remote-agent auto-deploy: deploy the latest revision of a flake between
# agent runs, roll back if the health check fails.
#
# Configured through the environment (set by the NixOS module):
#   RA_FLAKE           flake reference with attribute, e.g. github:o/r#droplet
#   RA_URL             app base URL, e.g. http://127.0.0.1:7700
#   RA_MAX_WAIT_SECS   how long to wait for the app to become idle
#   RA_POLL_SECS       idle poll interval (default 30)
#   RA_HEALTH_RETRIES  health check attempts after the switch (default 20)
#   RA_HEALTH_DELAY    seconds between health check attempts (default 6)
# Overridable commands (for tests):
#   RA_NIX, RA_NIXOS_REBUILD, RA_NIXOS_VERSION, RA_HEALTHCHECK
#   RA_SYSTEM_PROFILE  (default /nix/var/nix/profiles/system)
#   RA_STATE_DIR       remembers a revision that failed (default /var/lib/remote-agent-deploy)

: "${RA_FLAKE:?RA_FLAKE must be set}"
: "${RA_URL:=http://127.0.0.1:7700}"
: "${RA_MAX_WAIT_SECS:=21600}"
: "${RA_POLL_SECS:=30}"
: "${RA_HEALTH_RETRIES:=20}"
: "${RA_HEALTH_DELAY:=6}"
: "${RA_NIX:=nix}"
: "${RA_NIXOS_REBUILD:=nixos-rebuild}"
: "${RA_NIXOS_VERSION:=nixos-version}"
: "${RA_HEALTHCHECK:=remote-agent healthcheck --url $RA_URL}"
: "${RA_SYSTEM_PROFILE:=/nix/var/nix/profiles/system}"
: "${RA_STATE_DIR:=/var/lib/remote-agent-deploy}"
failed_file="$RA_STATE_DIR/failed-rev"

log() { echo "auto-deploy: $*" >&2; }

flake_ref="${RA_FLAKE%%#*}"
flake_attr=""
if [[ "$RA_FLAKE" == *"#"* ]]; then
  flake_attr="${RA_FLAKE#*#}"
fi

healthy() {
  # shellcheck disable=SC2086 # RA_HEALTHCHECK is a command line
  $RA_HEALTHCHECK >/dev/null 2>&1
}

wait_healthy() {
  local i
  for ((i = 1; i <= RA_HEALTH_RETRIES; i++)); do
    if healthy; then
      log "health check passed (attempt $i/$RA_HEALTH_RETRIES)"
      return 0
    fi
    log "health check failed (attempt $i/$RA_HEALTH_RETRIES)"
    sleep "$RA_HEALTH_DELAY"
  done
  return 1
}

drain() {
  curl -fsS --max-time 10 -X POST -H 'Content-Type: application/json' \
    -d "{\"drain\":$1}" "$RA_URL/api/drain" >/dev/null
}

undrain() {
  if drain false; then
    log "undrained: new runs may start"
  else
    log "WARNING: could not undrain the app (it resets on restart)"
  fi
}

idle() {
  curl -fsS --max-time 10 "$RA_URL/api/idle" | jq -e '.idle == true' >/dev/null
}

# 1. Is there anything to deploy?
if ! meta="$("$RA_NIX" flake metadata --json --refresh "$flake_ref")"; then
  log "ERROR: cannot resolve $flake_ref"
  exit 1
fi
latest_rev="$(jq -r '.revision // .locked.rev // empty' <<<"$meta")"
locked_url="$(jq -r '.url // empty' <<<"$meta")"
current_rev="$("$RA_NIXOS_VERSION" --configuration-revision 2>/dev/null || true)"

if [[ -z "$latest_rev" ]]; then
  log "ERROR: $flake_ref has no revision (dirty or not a git flake)"
  exit 1
fi
if [[ "$latest_rev" == "$current_rev" ]]; then
  log "up to date at $current_rev"
  exit 0
fi
if [[ -f "$failed_file" && "$(cat "$failed_file")" == "$latest_rev" ]]; then
  log "skipping $latest_rev: it failed its health check before (delete $failed_file to retry)"
  exit 0
fi
log "new revision: ${current_rev:-<none>} -> $latest_rev"

# Build exactly the revision we resolved, not whatever main is by now.
target="${locked_url:-$flake_ref}"
if [[ -n "$flake_attr" ]]; then
  target="$target#$flake_attr"
fi

# 2. Drain and wait for idle. An app that is down has no agents running
#    (they are its children), so it counts as idle.
was_healthy=true
if ! healthy; then
  was_healthy=false
  log "app is unhealthy before deploy; deploying without draining"
elif drain true; then
  log "draining: no new runs will start"
  waited=0
  until idle; do
    if ((waited >= RA_MAX_WAIT_SECS)); then
      log "gave up waiting for idle after ${waited}s; will retry next time"
      undrain
      exit 0
    fi
    log "waiting for running agents to finish (${waited}s)"
    sleep "$RA_POLL_SECS"
    waited=$((waited + RA_POLL_SECS))
  done
  log "app is idle"
else
  log "ERROR: drain request failed; not deploying"
  exit 1
fi

# 3. Switch.
old_profile="$(readlink "$RA_SYSTEM_PROFILE" || true)"
log "nixos-rebuild switch --flake $target"
switch_rc=0
"$RA_NIXOS_REBUILD" switch --flake "$target" || switch_rc=$?
new_profile="$(readlink "$RA_SYSTEM_PROFILE" || true)"

if [[ "$new_profile" == "$old_profile" ]]; then
  # Build or evaluation failed: nothing was activated, nothing to roll back.
  log "ERROR: nixos-rebuild failed with $switch_rc before activation; system unchanged"
  undrain
  exit 1
fi
if ((switch_rc != 0)); then
  log "WARNING: nixos-rebuild exited $switch_rc after activating $new_profile; deciding by health check"
fi

# 4. Health check, roll back on failure.
if wait_healthy; then
  log "deployed $latest_rev"
  rm -f "$failed_file"
  undrain
  exit 0
fi

if [[ "$was_healthy" == false ]]; then
  log "ERROR: app still unhealthy after deploying $latest_rev, but it was unhealthy before too; not rolling back"
  exit 1
fi

log "ERROR: health check failed after deploying $latest_rev; rolling back"
mkdir -p "$RA_STATE_DIR"
echo "$latest_rev" >"$failed_file"
if "$RA_NIXOS_REBUILD" switch --rollback; then
  log "rolled back to the previous generation"
else
  log "ERROR: nixos-rebuild switch --rollback failed"
fi
if wait_healthy; then
  log "app healthy after rollback"
else
  log "ERROR: app unhealthy after rollback"
fi
undrain
exit 1
