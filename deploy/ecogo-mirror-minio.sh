#!/usr/bin/env zsh
# Copy the MinIO image that runs in production to ghcr.io/arshadm25/minio, so it no longer depends
# on the copy cached on one node (quay.io/minio and Docker Hub minio/minio are not pullable any more).
# Needs no Docker. A one-off pod on the node that holds the image reads it from k3s's containerd and
# pushes it. The pod is deleted at the end. Touches only namespace minio.
# Token: a GitHub personal access token (classic) with write:packages. You type it; it is not echoed.
set -eu
KC="/Users/admin/Projects/Phase 2 - Travel/kubeconfig-hetzner.yml"
k() { kubectl --kubeconfig "$KC" "$@"; }
NS=minio
TARGET=ghcr.io/arshadm25/minio:mirror

NODE=$(k -n $NS get pod -l app=minio -o jsonpath='{.items[0].spec.nodeName}')
REF=$(k -n $NS get pod -l app=minio -o jsonpath='{.items[0].status.containerStatuses[0].imageID}')
echo "MinIO runs on $NODE from $REF"
printf "GitHub token (write:packages): "; read -rs PAT; echo
k -n $NS create secret generic mirror-ghcr --from-literal=pat="$PAT" --dry-run=client -o yaml | k apply -f -
unset PAT

k -n $NS delete pod minio-mirror --ignore-not-found
k apply -n $NS -f - <<YAML
apiVersion: v1
kind: Pod
metadata: { name: minio-mirror }
spec:
  nodeName: $NODE
  restartPolicy: Never
  containers:
    - name: mirror
      image: docker.io/library/busybox:stable
      env: [{ name: PAT, valueFrom: { secretKeyRef: { name: mirror-ghcr, key: pat } } }]
      command: [sh, -ec]
      args:
        - |
          CTR="/k3s ctr -a /run/k3s/containerd/containerd.sock -n k8s.io"
          \$CTR images tag --force "$REF" "$TARGET"
          \$CTR images push --user "arshadm25:\$PAT" "$TARGET"
      volumeMounts:
        - { name: sock, mountPath: /run/k3s/containerd/containerd.sock }
        - { name: k3s, mountPath: /k3s, readOnly: true }
  volumes:
    - { name: sock, hostPath: { path: /run/k3s/containerd/containerd.sock, type: Socket } }
    - { name: k3s, hostPath: { path: /usr/local/bin/k3s, type: File } }
YAML
k -n $NS wait --for=jsonpath='{.status.phase}'=Succeeded pod/minio-mirror --timeout=300s || { k -n $NS logs minio-mirror; exit 1; }
k -n $NS logs minio-mirror
k -n $NS delete pod minio-mirror
k -n $NS delete secret mirror-ghcr
echo "Done: $TARGET. In GitHub, set the package to public (or add a pull secret) and tell Claude."
