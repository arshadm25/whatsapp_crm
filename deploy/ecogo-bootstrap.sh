#!/usr/bin/env zsh
# Ecogo WhatsApp: first deploy to the Hetzner cluster. Run from your Mac, one block at a time.
# Touches only: namespaces cnpg-system, minio, ecogo-whatsapp (all new). Nothing existing is changed.
set -eu
k() { kubectl --kubeconfig "/Users/admin/Projects/Phase 2 - Travel/kubeconfig-hetzner.yml" "$@"; }
h() { helm --kubeconfig "/Users/admin/Projects/Phase 2 - Travel/kubeconfig-hetzner.yml" "$@"; }
SHA=e695d3e9655c1dea711eb61e48d155e1a0d609b2     # latest main; images are tagged by commit
NS=ecogo-whatsapp
rnd() { openssl rand -hex 24; }

echo "== 1. CloudNativePG operator (new namespace cnpg-system)"
h repo add cnpg https://cloudnative-pg.github.io/charts >/dev/null && h repo update cnpg >/dev/null
h upgrade --install cnpg cnpg/cloudnative-pg -n cnpg-system --create-namespace --wait

echo "== 2. MinIO for media (new namespace minio, 20Gi on local-path)"
k create namespace minio --dry-run=client -o yaml | k apply -f -
MINIO_USER=ecogo-minio; MINIO_PASS=$(rnd)
k -n minio create secret generic minio-root --from-literal=user=$MINIO_USER --from-literal=password=$MINIO_PASS \
  --dry-run=client -o yaml | k apply -f -
k apply -n minio -f - <<'YAML'
apiVersion: v1
kind: PersistentVolumeClaim
metadata: { name: minio-data }
spec:
  accessModes: [ReadWriteOnce]
  resources: { requests: { storage: 20Gi } }
---
apiVersion: apps/v1
kind: Deployment
metadata: { name: minio }
spec:
  replicas: 1
  strategy: { type: Recreate }
  selector: { matchLabels: { app: minio } }
  template:
    metadata: { labels: { app: minio } }
    spec:
      containers:
        - name: minio
          image: ghcr.io/arshadm25/minio:mirror   # copy made by deploy/ecogo-mirror-minio.sh; quay.io/minio is gone
          args: [server, /data, --console-address, ":9001"]
          env:
            - { name: MINIO_ROOT_USER, valueFrom: { secretKeyRef: { name: minio-root, key: user } } }
            - { name: MINIO_ROOT_PASSWORD, valueFrom: { secretKeyRef: { name: minio-root, key: password } } }
          ports: [{ containerPort: 9000 }]
          volumeMounts: [{ name: data, mountPath: /data }]
          resources: { requests: { cpu: 50m, memory: 128Mi }, limits: { memory: 512Mi } }
      volumes: [{ name: data, persistentVolumeClaim: { claimName: minio-data } }]
---
apiVersion: v1
kind: Service
metadata: { name: minio }
spec:
  selector: { app: minio }
  ports: [{ port: 9000, targetPort: 9000 }]
YAML

echo "== 3. Namespace and secrets (generated values are never printed)"
k create namespace $NS --dry-run=client -o yaml | k apply -f -
read -s "GHCR_USER?GitHub username for pulling images: "; echo
read -s "GHCR_TOKEN?GitHub token with read:packages (input hidden): "; echo
k -n $NS create secret docker-registry ghcr --docker-server=ghcr.io --docker-username="$GHCR_USER" \
  --docker-password="$GHCR_TOKEN" --dry-run=client -o yaml | k apply -f -
read -s "META_APP_SECRET?Meta App Secret (input hidden): "; echo
read -s "SMTP_PASSWORD?Mailtrap SMTP password (input hidden): "; echo
OWNER_PW=$(rnd); APP_PW=$(rnd)
k -n $NS create secret generic ecogo-whatsapp-db-owner --type=kubernetes.io/basic-auth \
  --from-literal=username=ecogo_owner --from-literal=password=$OWNER_PW --dry-run=client -o yaml | k apply -f -
k -n $NS create secret generic ecogo-whatsapp-db-app --type=kubernetes.io/basic-auth \
  --from-literal=username=ecogo_app --from-literal=password=$APP_PW --dry-run=client -o yaml | k apply -f -
PG=ecogo-whatsapp-pg-rw.$NS.svc:5432/ecogo?sslmode=require
k -n $NS create secret generic ecogo-whatsapp \
  --from-literal=database-url="postgres://ecogo_app:$APP_PW@$PG" \
  --from-literal=migration-database-url="postgres://ecogo_owner:$OWNER_PW@$PG" \
  --from-literal=master-keys="1:$(openssl rand -base64 32)" \
  --from-literal=app-secret="$(rnd)" \
  --from-literal=meta-app-secret="$META_APP_SECRET" \
  --from-literal=meta-webhook-verify-token="$(rnd)" \
  --from-literal=smtp-password="$SMTP_PASSWORD" \
  --from-literal=s3-access-key="$MINIO_USER" --from-literal=s3-secret-key="$MINIO_PASS" \
  --dry-run=client -o yaml | k apply -f -
unset GHCR_TOKEN META_APP_SECRET SMTP_PASSWORD OWNER_PW APP_PW MINIO_PASS

echo "== 4. Install the chart (run from a checkout of the repo)"
echo "Edit META_APP_ID, META_CONFIG_ID and SMTP_USER first, then run the helm command printed in the thread."
