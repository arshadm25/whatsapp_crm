#!/usr/bin/env zsh
# One-time switch from manual `helm upgrade` to Argo CD for the live release. Run from your Mac in
# a checkout of the repo, after the CI/CD change is merged and CI has finished on main (that run
# creates the production branch). Touches only: AppProject and Application ecogo-whatsapp in the
# Argo CD namespace, and (through Argo CD) the ecogo-whatsapp namespace. Safe to stop at the prompt.
set -eu
KC="/Users/admin/Projects/Phase 2 - Travel/kubeconfig-hetzner.yml"
k() { kubectl --kubeconfig "$KC" "$@"; }
h() { helm --kubeconfig "$KC" "$@"; }
NS=ecogo-whatsapp
ARGO_NS=${ARGO_NS:-argocd}
REPO=https://github.com/arshadm25/whatsapp_crm.git
URL=https://connect.ecogo.ai
cd "$(dirname "$0")/.."

echo "== 1. Checks"
k get ns $ARGO_NS >/dev/null || { echo "No namespace $ARGO_NS. Rerun with ARGO_NS=<Argo CD namespace>."; exit 1; }
k get crd applications.argoproj.io -o yaml | grep -q valuesObject \
  || { echo "This Argo CD is older than 2.6 (no helm.valuesObject); tell Claude the version."; exit 1; }
if k -n $ARGO_NS get application $NS >/dev/null 2>&1; then
  echo "Application $NS already exists; nothing to do."; exit 0
fi
PROD_SHA=$(git ls-remote $REPO refs/heads/production | cut -f1)
[ -n "$PROD_SHA" ] || { echo "No production branch yet: wait for CI on main to finish (job 'release')."; exit 1; }
echo "production branch is at $PROD_SHA"

echo "== 2. Save the live release's values (they become the Application's values)"
VALS=~/ecogo-whatsapp-values-$(date +%Y%m%d-%H%M%S).json
h get values $NS -n $NS -o json > "$VALS"
[ "$(cat "$VALS")" != "null" ] || { echo "Release $NS has no values; stopping."; exit 1; }
echo "Saved to $VALS (keep it: it is your fallback for a manual helm upgrade)."

echo "== 3. What Argo CD will change (expected: image tags, ECOGO_VERSION, Argo/keep annotations, a new migrate Job)"
CHART=$(mktemp -d)
git fetch -q origin production
git archive FETCH_HEAD deploy/helm | tar -x -C "$CHART"
h template $NS "$CHART/deploy/helm/ecogo-whatsapp" -n $NS -f "$VALS" \
  --set-string image.tag=$PROD_SHA --set-string webImage.tag=$PROD_SHA | k diff -n $NS -f - || true
rm -rf "$CHART"
read "OK?Hand the release to Argo CD now? [y/N] "
[ "$OK" = y ] || { echo "Stopped; nothing changed."; exit 0; }

echo "== 4. Create the Argo CD project and application"
APP=$(mktemp)
awk -v f="$VALS" -v ns="$ARGO_NS" '
  /^  namespace: argocd$/ { print "  namespace: " ns; next }
  /^ +valuesObject: \{\}$/ { getline j < f; sub(/valuesObject:.*/, ""); print $0 "valuesObject: " j; next }
  { print }' deploy/argocd/ecogo-whatsapp.yaml > "$APP"
k apply --dry-run=server -f "$APP" >/dev/null
k apply -f "$APP"
rm -f "$APP"

echo "== 5. Wait for the first sync (migrations run first; the old pods keep serving if anything fails)"
for i in {1..60}; do
  st=$(k -n $ARGO_NS get application $NS -o jsonpath='{.status.sync.status} {.status.health.status} {.status.operationState.phase}')
  echo "  $st"
  [[ "$st" == "Synced Healthy Succeeded" ]] && break
  sleep 10
done
echo "Live version: $(curl -fsS $URL/internal/config | grep -o '"version":"[^"]*"' || echo unknown) (want $PROD_SHA)"

cat <<TXT

Done when it says Synced Healthy Succeeded and the live version is $PROD_SHA. Last step, in GitHub:
  repo Settings > Secrets and variables > Actions > Variables > New repository variable
  name PRODUCTION_URL, value $URL
From now on every merge to main deploys itself; do not run helm upgrade on this release again.
TXT
