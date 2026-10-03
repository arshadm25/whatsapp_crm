#!/usr/bin/env bash
# Waits until the api at <url> reports <sha> as its version (from /internal/config), so a deploy
# that Argo CD could not finish (a failed migration, pods that never become ready) turns the
# workflow red. Usage: wait-for-version.sh https://whatsapp.ecogo.co.in <full commit sha>
set -euo pipefail
url=${1%/}; want=$2; timeout=${DEPLOY_TIMEOUT_SECONDS:-1200}
deadline=$((SECONDS + timeout)); seen=""
while ((SECONDS < deadline)); do
  seen=$(curl -fsS --max-time 10 "$url/internal/config" | jq -r '.version // empty' 2>/dev/null || true)
  if [[ $seen == "$want" ]]; then
    echo "$url is serving $want." | tee -a "${GITHUB_STEP_SUMMARY:-/dev/null}"
    exit 0
  fi
  sleep 20
done
echo "::error::$url still serves '${seen:-unknown}' after ${timeout}s, not $want. Check the ecogo-whatsapp app in Argo CD (a failed migration stops the sync; the previous version keeps running)."
exit 1
