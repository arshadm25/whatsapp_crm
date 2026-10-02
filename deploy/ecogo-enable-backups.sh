#!/usr/bin/env zsh
# Turn on nightly Postgres backups (CloudNativePG -> MinIO) for the live release.
# Backups go to the media bucket under pg-backups/ because the bucket already exists.
# They share MinIO's node-local disk, so this protects against mistakes, not against losing the node;
# copy them to a second EU site as the next step.
set -eu
KC="/Users/admin/Projects/Phase 2 - Travel/kubeconfig-hetzner.yml"
k() { kubectl --kubeconfig "$KC" "$@"; }
h() { helm --kubeconfig "$KC" "$@"; }
NS=ecogo-whatsapp

AK=$(k -n $NS get secret ecogo-whatsapp -o jsonpath='{.data.s3-access-key}' | base64 -d)
SK=$(k -n $NS get secret ecogo-whatsapp -o jsonpath='{.data.s3-secret-key}' | base64 -d)
k -n $NS create secret generic ecogo-whatsapp-backup-s3 \
  --from-literal=ACCESS_KEY_ID="$AK" --from-literal=ACCESS_SECRET_KEY="$SK" \
  --dry-run=client -o yaml | k apply -f -
unset AK SK

cd "$(dirname "$0")/.."
h upgrade ecogo-whatsapp deploy/helm/ecogo-whatsapp -n $NS --reuse-values \
  --set postgres.cnpg.backup.enabled=true \
  --set postgres.cnpg.backup.destinationPath=s3://ecogo-whatsapp-media/pg-backups/ \
  --set postgres.cnpg.backup.credentialsSecret=ecogo-whatsapp-backup-s3

echo "Backups enabled. Take one now and watch it finish:"
k -n $NS apply -f - <<YAML
apiVersion: postgresql.cnpg.io/v1
kind: Backup
metadata: { name: first-backup }
spec: { cluster: { name: ecogo-whatsapp-pg } }
YAML
echo "Check with: kubectl --kubeconfig \"$KC\" -n $NS get backup first-backup   (phase should become 'completed')"
