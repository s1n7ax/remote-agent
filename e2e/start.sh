#!/usr/bin/env bash
# Starts the fake GitHub and remote-agent (fake agents) in $E2E_DIR for the
# smoke test. Prints nothing on success; logs go to $E2E_DIR/*.log.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
dir=${E2E_DIR:?set E2E_DIR}
app_port=${APP_PORT:-17700}
gh_port=${GH_PORT:-17800}
mkdir -p "$dir/repos" "$dir/data"
: "${RA_BIN:=$dir/remote-agent}"
: "${FAKEGH_BIN:=$dir/fakegithub}"
if [ ! -f "$here/../web/dist/index.html" ]; then
  (cd "$here/../web" && npm ci --no-audit --no-fund && npm run build) >&2
fi
[ -x "$RA_BIN" ] || go build -o "$RA_BIN" "$here/../cmd/remote-agent"
[ -x "$FAKEGH_BIN" ] || go build -o "$FAKEGH_BIN" "$here/fakegithub"
echo fake-token > "$dir/gh-token"
echo e2e-login-token > "$dir/login-token"
cat > "$dir/config.json" <<JSON
{
  "listen": "127.0.0.1:$app_port",
  "mcpListen": "127.0.0.1:$((app_port + 1))",
  "previewListen": "127.0.0.1:$((app_port + 2))",
  "dataDir": "$dir/data",
  "githubTokenFile": "$dir/gh-token",
  "loginTokenFile": "$dir/login-token",
  "githubApiUrl": "http://127.0.0.1:$gh_port/api",
  "githubGraphqlUrl": "http://127.0.0.1:$gh_port/api/graphql",
  "gitUrl": "http://127.0.0.1:$gh_port/git",
  "skillsSeedDir": "$here/../skills",
  "sandboxPath": "$PATH",
  "ciPollSeconds": 1,
  "fakeAgents": true
}
JSON
"$FAKEGH_BIN" -listen "127.0.0.1:$gh_port" -root "$dir/repos" -seed e2e/demo,e2e/other > "$dir/fakegithub.log" 2>&1 &
echo $! > "$dir/fakegithub.pid"
FAKE_AGENT_SCRIPT_DIR="$here/agents" "$RA_BIN" serve --config "$dir/config.json" > "$dir/app.log" 2>&1 &
echo $! > "$dir/app.pid"
for _ in $(seq 50); do
  "$RA_BIN" healthcheck --url "http://127.0.0.1:$app_port" 2>/dev/null && exit 0
  sleep 0.2
done
echo "remote-agent did not become healthy" >&2
cat "$dir/app.log" >&2
exit 1
