#!/usr/bin/env zsh
# Move Postgres backups from CloudNativePG's deprecated built-in Barman support to the Barman Cloud
# plugin. Run from your Mac in a checkout of the repo after pulling main.
#   1. checks the operator version and cert-manager
#   2. installs the plugin into cnpg-system (new Deployment barman-cloud; nothing existing changes)
#   3. switches this release to the plugin (helm upgrade, or an Argo CD patch if Argo manages it)
#   4. takes a backup through the plugin and waits for it
# Existing backups and WAL stay readable: the plugin uses the same bucket path.
# Expect the two database pods to restart one after the other (the plugin adds a sidecar), with a
# short failover of the primary. Do it at a quiet time.
# Set PLUGIN_VERSION=vX.Y.Z to choose the plugin release; the default is the latest.
set -eu
KC="/Users/admin/Projects/Phase 2 - Travel/kubeconfig-hetzner.yml"
k() { kubectl --kubeconfig "$KC" "$@"; }
h() { helm --kubeconfig "$KC" "$@"; }
NS=ecogo-whatsapp
OP_NS=cnpg-system
ARGO_NS=${ARGO_NS:-argocd}
cd "$(dirname "$0")/.."

echo "== 1. Checks"
IMAGES=$(k -n $OP_NS get deploy -o jsonpath='{.items[*].spec.template.spec.containers[*].image}' | tr ' ' '\n')
echo "Images in $OP_NS:"; echo "$IMAGES" | sed 's/^/  /'
# The operator image ends in /cloudnative-pg:<version>; the plugin's is .../plugin-barman-cloud:<version>.
OP_IMAGE=$(echo "$IMAGES" | grep -E '/cloudnative-pg:' | head -1)
[ -n "$OP_IMAGE" ] || { echo "No CloudNativePG operator image found in $OP_NS. Tell Claude."; exit 1; }
OP_VER=${OP_IMAGE##*:}; OP_VER=${OP_VER#v}
echo "Operator version: $OP_VER"
OP_MINOR=$(echo "$OP_VER" | cut -d. -f2)
[[ "$(echo "$OP_VER" | cut -d. -f1)" -ge 1 && "$OP_MINOR" -ge 26 ]] \
  || { echo "The plugin needs CloudNativePG 1.26 or newer; this is $OP_VER. Stop and tell Claude."; exit 1; }
k get crd certificates.cert-manager.io >/dev/null || { echo "cert-manager is required by the plugin and is not installed."; exit 1; }
k -n $NS get cluster ecogo-whatsapp-pg >/dev/null
k -n $NS get backup first-backup -o jsonpath='{.status.phase}' | grep -q completed \
  || echo "Warning: the earlier first-backup is not 'completed'; check backups before going on."

echo "== 2. Install the Barman Cloud plugin"
if echo "$IMAGES" | grep -q plugin-barman-cloud; then
  echo "The plugin is already installed ($(echo "$IMAGES" | grep plugin-barman-cloud | head -1)); skipping the install."
  k -n $OP_NS get deploy
else
V=${PLUGIN_VERSION:-$(curl -fsSL https://api.github.com/repos/cloudnative-pg/plugin-barman-cloud/releases/latest | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)}
[ -n "$V" ] || { echo "Could not find the latest plugin release; rerun with PLUGIN_VERSION=vX.Y.Z"; exit 1; }
echo "Plugin version: $V"
k apply -f "https://github.com/cloudnative-pg/plugin-barman-cloud/releases/download/$V/manifest.yaml"
k -n $OP_NS rollout status deployment/barman-cloud --timeout=180s
fi

echo
printf "Switch %s backups to the plugin now? Database pods will restart one by one. [y/N] " "$NS"
read -r ANS; [[ "$ANS" == y || "$ANS" == Y ]] || { echo "Stopped after installing the plugin. Nothing else changed."; exit 0; }

echo "== 3. Switch the release to the plugin"
if k -n $ARGO_NS get application $NS >/dev/null 2>&1; then
  echo "Argo CD manages this release; patching its values."
  k -n $ARGO_NS patch application $NS --type merge \
    -p '{"spec":{"source":{"helm":{"valuesObject":{"postgres":{"cnpg":{"backup":{"method":"plugin"}}}}}}}}'
  echo "Waiting for Argo CD to sync..."
  sleep 20
  k -n $ARGO_NS wait application/$NS --for=jsonpath='{.status.sync.status}'=Synced --timeout=300s
else
  h upgrade $NS deploy/helm/ecogo-whatsapp -n $NS --reuse-values --set postgres.cnpg.backup.method=plugin
fi
k -n $NS get objectstore
k -n $NS wait cluster/ecogo-whatsapp-pg --for=condition=Ready --timeout=600s

echo "== 4. Take a backup through the plugin"
NAME=plugin-first-backup-$(date +%H%M%S)
k -n $NS apply -f - <<YAML
apiVersion: postgresql.cnpg.io/v1
kind: Backup
metadata: { name: $NAME }
spec:
  method: plugin
  cluster: { name: ecogo-whatsapp-pg }
  pluginConfiguration: { name: barman-cloud.cloudnative-pg.io }
YAML
for _ in {1..60}; do
  PHASE=$(k -n $NS get backup $NAME -o jsonpath='{.status.phase}')
  echo "backup $NAME: ${PHASE:-starting}"
  [[ $PHASE == completed || $PHASE == failed ]] && break
  sleep 10
done
[[ $PHASE == completed ]] && echo "Done: backups now run through the plugin." || { echo "Backup did not complete; paste the output above to Claude."; exit 1; }
